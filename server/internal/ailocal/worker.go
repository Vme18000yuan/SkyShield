package ailocal

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// Main is entered before the product reads configuration or connects to a DB.
func Main(arguments []string) int {
	flags := flag.NewFlagSet("ai-tool-worker", flag.ContinueOnError)
	root := flags.String("root", "", "owned workspace")
	stage := flags.String("stage", "launch", "worker stage")
	external := flags.String("exec-json", "", "external command argument vector")
	environmentJSON := flags.String("env-json", "", "external command environment")
	environmentFile := flags.String("env-file", "", "private external command environment file")
	readPathsJSON := flags.String("read-paths", "", "explicit read-only interpreter resources")
	groupReport := flags.String("group-report", "", "parent pipe for MCP process group identity")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	absolute, err := filepath.Abs(*root)
	if err != nil || !filepath.IsAbs(*root) || absolute == "/" {
		fmt.Fprintln(os.Stderr, "invalid tool workspace")
		return 2
	}
	if err = os.MkdirAll(filepath.Join(absolute, ".tmp"), 0700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err = os.Chdir(absolute); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *stage == "launch" {
		if *groupReport != "" {
			expected := fmt.Sprintf("/proc/%d/fd/", os.Getppid())
			if *external == "" || !strings.HasPrefix(*groupReport, expected) {
				fmt.Fprintln(os.Stderr, "invalid MCP process group pipe")
				return 2
			}
			report, err := os.OpenFile(*groupReport, os.O_WRONLY, 0)
			if err != nil {
				fmt.Fprintln(os.Stderr, "cannot report MCP process group")
				return 1
			}
			info, err := report.Stat()
			if err != nil || info.Mode()&os.ModeNamedPipe == 0 || createMCPProcessSession() != nil {
				report.Close()
				fmt.Fprintln(os.Stderr, "cannot create MCP process group")
				return 1
			}
			_, err = fmt.Fprintln(report, os.Getpid())
			_ = report.Close()
			if err != nil {
				return 1
			}
		}
		if *environmentFile != "" {
			expected := fmt.Sprintf("/proc/%d/fd/", os.Getppid())
			if *environmentJSON != "" || !strings.HasPrefix(*environmentFile, expected) {
				fmt.Fprintln(os.Stderr, "invalid MCP environment pipe")
				return 2
			}
			file, err := os.Open(*environmentFile)
			if err != nil {
				fmt.Fprintln(os.Stderr, "cannot read MCP environment")
				return 1
			}
			info, err := file.Stat()
			if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
				file.Close()
				fmt.Fprintln(os.Stderr, "invalid MCP environment")
				return 1
			}
			raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
			_ = file.Close()
			if err != nil || len(raw) > 1<<20 {
				fmt.Fprintln(os.Stderr, "cannot read MCP environment")
				return 1
			}
			*environmentJSON = string(raw)
		}
		executable, err := os.Executable()
		if err == nil {
			arguments := []string{"ai-tool-worker", "--root", absolute, "--stage", "serve"}
			environment := os.Environ()
			if *external != "" {
				var values map[string]string
				if json.Unmarshal([]byte(*environmentJSON), &values) != nil {
					fmt.Fprintln(os.Stderr, "invalid MCP environment")
					return 2
				}
				environment = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "GOMAXPROCS=2", "HOME=" + absolute, "TMPDIR=" + filepath.Join(absolute, ".tmp"), "LANG=C.UTF-8"}
				for key, value := range values {
					if strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
						return 2
					}
					environment = append(environment, key+"="+value)
				}
				arguments = []string{"ai-tool-worker", "--root", absolute, "--stage", "external", "--exec-json", *external}
			}
			var readPaths []string
			if *readPathsJSON != "" && (json.Unmarshal([]byte(*readPathsJSON), &readPaths) != nil || len(readPaths) > 128) {
				fmt.Fprintln(os.Stderr, "invalid sandbox resources")
				return 2
			}
			err = enterSandbox(absolute, executable, arguments, environment, readPaths)
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *stage == "external" {
		var command []string
		if json.Unmarshal([]byte(*external), &command) != nil || len(command) == 0 {
			return 2
		}
		path, err := exec.LookPath(command[0])
		if err == nil {
			err = syscall.Exec(path, command, os.Environ())
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if *stage != "serve" {
		return 2
	}
	if err = serveTools(absolute, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
func within(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}
func serveTools(root string, input io.Reader, output io.Writer) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	manager, err := tool.NewManager(uuid.NewString())
	if err != nil {
		return err
	}
	defer manager.Cleanup()
	registry := tool.NewRegistry(NativeTools(true)...)
	var outputMu sync.Mutex
	encoder := json.NewEncoder(output)
	respond := func(value Response) { outputMu.Lock(); defer outputMu.Unlock(); _ = encoder.Encode(value) }
	respond(Response{Ready: true})
	var running sync.Map
	var workers sync.WaitGroup
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 64*1024), 8<<20)
		for scanner.Scan() {
			var request Request
			if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
				done <- err
				return
			}
			if request.Cancel != "" {
				if cancel, ok := running.Load(request.Cancel); ok {
					cancel.(context.CancelFunc)()
				}
				continue
			}
			runCtx, stop := context.WithTimeout(ctx, workerToolTimeout)
			running.Store(request.ID, stop)
			workers.Add(1)
			go func(request Request) {
				defer workers.Done()
				defer stop()
				defer running.Delete(request.ID)
				response := Response{ID: request.ID}
				defer func() {
					if value := recover(); value != nil {
						response.Error = fmt.Sprintf("tool error: %v", value)
					}
					respond(response)
				}()
				cwd := request.WorkingDir
				if cwd == "" {
					cwd = root
				}
				if !within(root, cwd) || request.OutputDir != "" && !within(root, request.OutputDir) {
					response.Error = "工具目录超出当前用户工作区"
					return
				}
				if request.Name == "__review_context" {
					var review struct {
						Tool       string                     `json:"tool"`
						Arguments  json.RawMessage            `json:"arguments"`
						Background intercept.ReviewBackground `json:"background"`
					}
					if json.Unmarshal(request.Input, &review) != nil || review.Tool == "" {
						response.Error = "invalid review request"
						return
					}
					reviewCtx := intercept.WithReviewContext(runCtx, cwd, review.Background)
					value, err := intercept.BuildReviewInput(reviewCtx, review.Tool, review.Arguments)
					if err != nil {
						response.Error = "审查上下文不完整"
						return
					}
					raw, err := json.Marshal(value)
					if err != nil {
						response.Error = "审查上下文编码失败"
						return
					}
					response.Result = tool.Text(string(raw))
					return
				}
				item, ok := registry.Get(request.Name)
				if !ok {
					response.Error = "unknown local tool"
					return
				}
				if err := tool.ValidateInput(item.InputSchema(), request.Input); err != nil {
					response.Error = err.Error()
					return
				}
				decision := item.CheckPermissions(runCtx, request.Input, permission.Context{Mode: permission.ModeBypass, WorkingDir: cwd})
				if decision.Behavior == permission.Deny {
					response.Result = tool.Errorf(decision.Message)
					return
				}
				var callErr error
				response.Result, callErr = item.Call(runCtx, request.Input, &tool.ToolContext{WorkingDir: cwd, OutputDir: request.OutputDir, MaxOutputChars: request.MaxOutputChars, ToolUseID: request.ToolUseID, AgentID: request.AgentID, Tasks: manager, Env: request.Env, Emit: func(progress tool.ProgressInfo) { respond(Response{ID: request.ID, Progress: progress.Message}) }})
				if callErr != nil {
					response.Error = callErr.Error()
				}
				response.Notifications = manager.DrainNotifications()
			}(request)
		}
		done <- scanner.Err()
	}()
	select {
	case err = <-done:
	case <-ctx.Done():
		err = ctx.Err()
	}
	cancel()
	running.Range(func(_, value any) bool { value.(context.CancelFunc)(); return true })
	manager.KillAll()
	workers.Wait()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
