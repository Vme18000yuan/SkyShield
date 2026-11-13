// Package stream connects incremental tool observations to typed result sinks.
// It owns bounded I/O and cancellation only; parsers and scope remain with each
// Engine and the Server.
package stream

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// Drain validates completion of a stream whose observations have already been
// acknowledged through live delivery. It avoids a second identical upload.
func Drain[T any](ctx context.Context, items <-chan T) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, open := <-items:
			if !open {
				return nil
			}
		}
	}
}

// Submit keeps a result stream open while produce runs. A producer failure
// closes the stream and flushes valid observations before returning the failure.
// A delivery failure cancels the producer. Parent cancellation is authoritative.
func Submit[T any](ctx context.Context, submit func(context.Context, <-chan T) error, produce func(context.Context, func(T) error) error) error {
	if ctx == nil || submit == nil || produce == nil {
		return errors.New("stream context, submitter and producer are required")
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	items := make(chan T)
	done := make(chan error, 1)
	go func() {
		err := submit(workCtx, items)
		cancel()
		done <- err
	}()
	produceErr := produce(workCtx, func(item T) error {
		select {
		case <-workCtx.Done():
			return workCtx.Err()
		case items <- item:
			return nil
		}
	})
	close(items)
	submitErr := <-done
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if submitErr != nil {
		return fmt.Errorf("submit live results: %w", submitErr)
	}
	return produceErr
}

// File follows complete lines from a growing, task-local file during run.
// Partial lines are retained until completed, oversized lines are bounded and
// reported to visit, and the final file tail is drained even if run fails.
func File(ctx context.Context, path string, limit int, run func(context.Context) error, visit func(string, bool) error) error {
	if ctx == nil || path == "" || limit < 1 || run == nil || visit == nil {
		return errors.New("live file inputs are required")
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(workCtx) }()
	var file *os.File
	var reader *bufio.Reader
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()
	pending := make([]byte, 0, min(limit, 64*1024))
	oversized := false
	drain := func(final bool) error {
		if file == nil {
			var err error
			file, err = os.Open(path)
			if errors.Is(err, os.ErrNotExist) {
				file = nil
				return nil
			}
			if err != nil {
				return fmt.Errorf("open live output: %w", err)
			}
			info, err := file.Stat()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return errors.New("live output must be a regular file")
			}
			reader = bufio.NewReaderSize(file, 64*1024)
		}
		for {
			if err := workCtx.Err(); err != nil {
				return err
			}
			part, err := reader.ReadSlice('\n')
			complete := len(part) > 0 && part[len(part)-1] == '\n'
			if complete {
				part = part[:len(part)-1]
			}
			if !oversized {
				if len(part) > limit-len(pending) {
					pending = pending[:0]
					oversized = true
				} else {
					pending = append(pending, part...)
				}
			}
			if complete || final && errors.Is(err, io.EOF) && (len(pending) > 0 || oversized) {
				if e := visit(string(pending), oversized); e != nil {
					return e
				}
				pending = pending[:0]
				oversized = false
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
				return fmt.Errorf("read live output: %w", err)
			}
		}
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case runErr := <-done:
			return errors.Join(runErr, drain(true))
		case <-ctx.Done():
			cancel()
			<-done
			return ctx.Err()
		case <-ticker.C:
			if err := drain(false); err != nil {
				cancel()
				<-done
				return err
			}
		}
	}
}
