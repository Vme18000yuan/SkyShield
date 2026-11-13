package urlcollectionruntime

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/yyhuni/lunafox/engine-go/stream"
	contract "github.com/yyhuni/lunafox/engines/url_collection/contract"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Collection, cleanup and verification overlap. Disk-backed tool output plus a
// bounded channel provides backpressure without an input-sized memory ledger.
func (r *Runtime) executeLive(ctx context.Context, execution *contract.Execution, paths pipelinePaths, seeds uint64) error {
	parentCtx := ctx
	var collectorBudget int64
	if execution.Config.Katana.Enabled {
		collectorBudget = execution.Config.Katana.Timeout
	}
	if execution.Config.Waymore.Enabled && execution.Target.Type == contract.TargetTypeDomain {
		collectorBudget = max(collectorBudget, execution.Config.Waymore.Timeout)
	}
	budget := collectorBudget
	if execution.Config.Uro.Enabled {
		budget += execution.Config.Uro.Timeout
	}
	if execution.Config.HTTPX.Enabled {
		budget += execution.Config.HTTPX.Timeout
	}
	if budget <= 0 {
		return errors.New("URL pipeline budget must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(budget)*time.Second)
	defer cancel()
	var discovered, verified, batches atomic.Uint64
	var progressMu sync.Mutex
	report := func(message string) error {
		progressMu.Lock()
		defer progressMu.Unlock()
		return execution.Progress.Report(ctx, message)
	}
	started := time.Now()
	heartbeatDone := make(chan struct{})
	heartbeatErr := make(chan error, 1)
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					return
				}
				if err := report(fmt.Sprintf("url_progress seeds=%d discovered=%d verified=%d batches=%d elapsedSeconds=%d", seeds, discovered.Load(), verified.Load(), batches.Load(), int(time.Since(started).Seconds()))); err != nil {
					if ctx.Err() != nil {
						return
					}
					heartbeatErr <- err
					cancel()
					return
				}
			}
		}
	}()
	err := stream.Submit(ctx, execution.Results.EndpointV2.Submit, func(liveCtx context.Context, emit func(contract.EndpointV2) error) error {
		urls := make(chan string, 128)
		collectorDone := make(chan error, 1)
		collectorCtx, stopCollectors := context.WithCancel(liveCtx)
		defer stopCollectors()
		go func() {
			collectorDone <- r.collectLive(collectorCtx, execution, paths, report, func(raw string) error {
				select {
				case urls <- raw:
					discovered.Add(1)
					return nil
				case <-collectorCtx.Done():
					return collectorCtx.Err()
				}
			})
			close(urls)
		}()
		batch := make([]string, 0, 128)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			ordinal := batches.Add(1)
			if err := report(fmt.Sprintf("url_batch started batch=%d candidates=%d", ordinal, len(batch))); err != nil {
				return err
			}
			if err := r.verifyLiveBatch(liveCtx, execution, batch, ordinal, func(item contract.EndpointV2) error {
				if err := emit(item); err != nil {
					return err
				}
				verified.Add(1)
				return nil
			}); err != nil {
				return err
			}
			batch = batch[:0]
			return report(fmt.Sprintf("url_batch completed batch=%d verifiedTotal=%d", ordinal, verified.Load()))
		}
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-liveCtx.Done():
				stopCollectors()
				<-collectorDone
				return liveCtx.Err()
			case raw, open := <-urls:
				if !open {
					return errors.Join(<-collectorDone, flush())
				}
				batch = append(batch, raw)
				if len(batch) == cap(batch) {
					if err := flush(); err != nil {
						stopCollectors()
						<-collectorDone
						return err
					}
				}
			case <-ticker.C:
				if err := flush(); err != nil {
					stopCollectors()
					<-collectorDone
					return err
				}
			}
		}
	})
	cancel()
	<-heartbeatDone
	select {
	case progressErr := <-heartbeatErr:
		return errors.Join(err, progressErr)
	default:
	}
	if err != nil {
		if parentCtx.Err() == nil {
			reason := "stage_failure"
			if errors.Is(err, context.DeadlineExceeded) {
				reason = "time_budget"
			}
			_ = execution.Progress.Report(parentCtx, fmt.Sprintf("partial_results observedEndpoints=%d batches=%d reason=%s acknowledged_batches_retained=true", verified.Load(), batches.Load(), reason))
		}
		return err
	}
	return execution.Progress.Report(parentCtx, fmt.Sprintf("completed urlSeedRecords=%d discoveredURLs=%d verifiedEndpoints=%d batches=%d", seeds, discovered.Load(), verified.Load(), batches.Load()))
}

func (r *Runtime) collectLive(ctx context.Context, execution *contract.Execution, paths pipelinePaths, report func(string) error, emit func(string) error) error {
	type collector struct {
		name, path string
		command    toolCommand
	}
	var collectors []collector
	if execution.Config.Waymore.Enabled && execution.Target.Type == contract.TargetTypeDomain {
		cmd, err := buildWaymoreCommand(execution.Target, paths.waymore, execution.Config.Waymore)
		if err != nil {
			return err
		}
		collectors = append(collectors, collector{"waymore", paths.waymore, cmd})
	}
	if execution.Config.Katana.Enabled {
		cmd, err := buildKatanaCommand(paths.websiteSeeds, paths.katana, execution.Config.Katana)
		if err != nil {
			return err
		}
		collectors = append(collectors, collector{"katana", paths.katana, cmd})
	}
	if len(collectors) == 0 {
		return errors.New("no enabled URL Collection collector is applicable")
	}
	var mu sync.Mutex
	seen := make(map[[32]byte]bool)
	ring := make([][32]byte, 8192)
	cursor := 0
	admit := func(raw string, oversized bool) error {
		if oversized || raw == "" || !InTargetScope(execution.Target, raw) {
			return nil
		}
		hash := sha256.Sum256([]byte(raw))
		mu.Lock()
		if seen[hash] {
			mu.Unlock()
			return nil
		}
		if len(seen) == len(ring) {
			delete(seen, ring[cursor])
		}
		ring[cursor] = hash
		cursor = (cursor + 1) % len(ring)
		seen[hash] = true
		mu.Unlock()
		return emit(raw)
	}
	var wait sync.WaitGroup
	failures := make(chan error, len(collectors))
	for _, item := range collectors {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := report(item.name + " started"); err != nil {
				failures <- err
				return
			}
			livePath := item.path
			if item.command.liveOutput != "" {
				livePath = item.command.liveOutput
			}
			err := stream.File(ctx, livePath, maxRecordBytes, func(toolCtx context.Context) error { return r.executor.run(toolCtx, item.command) }, admit)
			// Reconcile the tool's final artifact as well. This covers records
			// emitted only to -o, and exact repeats are suppressed by the cache.
			if info, statErr := os.Stat(item.path); statErr == nil && info.Mode().IsRegular() {
				err = errors.Join(err, ReadBoundedLines(item.path, admit))
			}
			if err == nil {
				err = requireRegularOutput(item.path)
			}
			if err != nil {
				failures <- fmt.Errorf("%s collection: %w", item.name, err)
				return
			}
			if err := report(item.name + " completed"); err != nil {
				failures <- err
			}
		}()
	}
	wait.Wait()
	close(failures)
	var errs []error
	for err := range failures {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (r *Runtime) verifyLiveBatch(ctx context.Context, execution *contract.Execution, urls []string, ordinal uint64, emit func(contract.EndpointV2) error) error {
	workspace := filepath.Join(execution.Workspace, fmt.Sprintf("url-batch-%08d", ordinal))
	if err := os.Mkdir(workspace, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(workspace)
	paths := pipelinePaths{workspace: workspace}
	if err := paths.prepare(); err != nil {
		return err
	}
	if err := os.WriteFile(paths.collectorCandidates, []byte(strings.Join(urls, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	batchExecution := *execution
	batchExecution.Progress = quietProgress{}
	probe, _, err := r.runUro(ctx, &batchExecution, paths)
	if err != nil || probe == "" {
		return err
	}
	if !execution.Config.HTTPX.Enabled {
		return ReadBoundedLines(probe, func(raw string, oversized bool) error {
			if oversized {
				return errors.New("oversized probe URL")
			}
			if raw == "" {
				return nil
			}
			item, err := observedEndpointFromURL(raw)
			if err != nil {
				return err
			}
			return emit(item)
		})
	}
	output := filepath.Join(workspace, "live-httpx.jsonl")
	command, err := buildHTTPXCommand(probe, output, execution.Config.HTTPX)
	if err != nil {
		return err
	}
	var records, structural int
	err = stream.File(ctx, output, maxRecordBytes, func(toolCtx context.Context) error { return r.executor.run(toolCtx, command) }, func(line string, oversized bool) error {
		if oversized {
			return nil
		}
		if strings.TrimSpace(line) == "" {
			return nil
		}
		records++
		outcome := ParseHTTPXRecord(line)
		if outcome.Structural {
			structural++
		}
		if outcome.Skip == SkipUnassociatedIdentity {
			return errors.New("httpx output row has no candidate input identity")
		}
		if outcome.Skip != "" || !InTargetScope(execution.Target, outcome.Endpoint.URL) {
			return nil
		}
		return emit(outcome.Endpoint)
	})
	if err != nil {
		return err
	}
	if err := requireRegularOutput(output); err != nil {
		return err
	}
	if records > 0 && structural == 0 {
		return errors.New("httpx output contains no structurally valid records")
	}
	return nil
}

type quietProgress struct{}

func (quietProgress) Report(context.Context, string) error { return nil }
