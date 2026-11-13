package websitediscoveryruntime

import (
	"context"
	"fmt"
	"github.com/yyhuni/lunafox/engine-go/stream"
	resultparser "github.com/yyhuni/lunafox/engines/website_discovery/internal/results"
	"path/filepath"
	"strings"
	"time"

	websitediscoverycontract "github.com/yyhuni/lunafox/engines/website_discovery/contract"
)

type Runtime struct {
	results websitediscoverycontract.WebsiteV2Submitter
}

func New() *Runtime {
	return &Runtime{}
}

func NewWithResults(results websitediscoverycontract.WebsiteV2Submitter) *Runtime {
	return &Runtime{results: results}
}

func (r *Runtime) Name() string { return websitediscoverycontract.Name }

type resultArtifacts struct {
	livePublished      bool
	resultArtifactPath string
	workspaceDir       string
}

// Execute consumes the finalized HostPorts projection and runs image-owned httpx.
func (r *Runtime) Execute(
	ctx context.Context,
	config websitediscoverycontract.Config,
	target websitediscoverycontract.Target,
	hostPortsPath string,
	workDir string,
	progress ProgressReporter,
) (*resultArtifacts, error) {
	if ctx == nil {
		return nil, fmt.Errorf("execution context is required")
	}
	if progress == nil {
		return nil, fmt.Errorf("engine progress port is required")
	}
	run, err := initializeWebsiteDiscovery(ctx, config, target, hostPortsPath, workDir, progress, containerHTTPXExecutor{})
	if err != nil {
		return nil, err
	}
	outputPath, err := r.runHTTPXStage(ctx, run)
	if err != nil {
		return &resultArtifacts{workspaceDir: workDir}, err
	}
	if outputPath == "" {
		return &resultArtifacts{workspaceDir: workDir}, nil
	}
	return &resultArtifacts{resultArtifactPath: outputPath, workspaceDir: workDir, livePublished: r.results != nil}, nil
}

type websiteDiscoveryRun struct {
	config      websitediscoverycontract.Config
	urlListPath string
	urlCount    uint64
	workDir     string
	progress    ProgressReporter
	executor    httpxExecutor
}

func initializeWebsiteDiscovery(
	ctx context.Context,
	config websitediscoverycontract.Config,
	target websitediscoverycontract.Target,
	hostPortsPath string,
	workDir string,
	progress ProgressReporter,
	executor httpxExecutor,
) (*websiteDiscoveryRun, error) {
	if executor == nil {
		return nil, fmt.Errorf("httpx executor is required")
	}
	candidatePath, urlCount, err := prepareTargetCandidates(ctx, target, hostPortsPath, workDir)
	if err != nil {
		return nil, err
	}
	return &websiteDiscoveryRun{config: config, urlListPath: candidatePath, urlCount: urlCount, workDir: workDir, progress: progress, executor: executor}, nil
}

func (r *Runtime) runHTTPXStage(ctx context.Context, run *websiteDiscoveryRun) (string, error) {
	if !run.config.HTTPX.Enabled {
		return "", run.progress.Report(ctx, "skip stage 1/1 httpx reason=disabled")
	}
	outputPath := filepath.Join(run.workDir, "httpx.jsonl")
	command, err := buildHTTPXCommand(run.urlListPath, outputPath, run.config.HTTPX)
	if err != nil {
		return "", err
	}
	if err := run.progress.Report(ctx, fmt.Sprintf("start stage 1/1 httpx: probe websites urls=%d", run.urlCount)); err != nil {
		return "", err
	}
	if err := run.progress.Report(ctx, fmt.Sprintf("start command stage=httpx label=httpx command=%s", renderHTTPXCommand(command))); err != nil {
		return "", err
	}
	startedAt := time.Now()
	runCommand := func(toolCtx context.Context) error { return run.executor.run(toolCtx, command) }
	if r.results != nil {
		original := runCommand
		runCommand = func(toolCtx context.Context) error {
			return stream.Submit(toolCtx, r.results.Submit, func(liveCtx context.Context, emit func(websitediscoverycontract.WebsiteV2) error) error {
				return stream.File(liveCtx, outputPath, 4*1024*1024, original, func(line string, oversized bool) error {
					if oversized {
						return nil
					}
					item, ok, err := resultparser.ParseLiveHTTPXLine(line)
					if err != nil {
						return err
					}
					if !ok {
						return nil
					}
					return emit(item)
				})
			})
		}
	}
	if err := runCommand(ctx); err != nil {
		_ = run.progress.Report(ctx, "fail stage 1/1 httpx reason=command: "+err.Error())
		return "", err
	}
	if err := run.progress.Report(ctx, fmt.Sprintf("complete command stage=httpx label=httpx exitCode=0 duration=%s", time.Since(startedAt).Round(time.Millisecond))); err != nil {
		return "", err
	}
	if err := run.progress.Report(ctx, "complete stage 1/1 httpx success=1 failed=0 outputs=1"); err != nil {
		return "", err
	}
	return outputPath, nil
}

func renderHTTPXCommand(command httpxCommand) string {
	parts := []string{"httpx"}
	maskNext := false
	for _, arg := range command.args {
		// Progress is persisted outside the Engine; execution keeps the real argv.
		if maskNext {
			parts = append(parts, "[REDACTED]")
			maskNext = false
			continue
		}
		parts = append(parts, quoteCommandPartForProgress(arg))
		maskNext = arg == "-H"
	}
	return strings.Join(parts, " ")
}

func quoteCommandPartForProgress(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\n\r'\"") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
