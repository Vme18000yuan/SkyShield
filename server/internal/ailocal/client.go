package ailocal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	command          *exec.Cmd
	input            io.WriteCloser
	encoder          *json.Encoder
	writeGate        chan struct{}
	toolSlots        chan struct{}
	controlSlots     chan struct{}
	pending          sync.Map
	done             chan struct{}
	transportDone    chan struct{}
	responseTimeout  time.Duration
	closeOnce        sync.Once
	releaseResources func()
}

var ErrTransportLost = errors.New("本地工具执行器通信已中断，无法确认本次调用是否完成；请核对现有结果后再决定后续操作，不要直接重复有副作用的操作")
var ErrResponseTimeout = errors.New("本地工具执行器未在工具运行时限及通信宽限期内返回，已关闭异常执行器；本次调用的结果不确定，请先核对已有输出")

type pendingCall struct {
	result   chan Response
	progress chan string
}

func NewClient(ctx context.Context, root, executable string, environment []string) (*Client, error) {
	return NewClientWithReadPaths(ctx, root, executable, environment, nil)
}

// NewClientWithReadPaths grants only read/execute access to explicitly resolved
// interpreter resources. General-purpose local tools keep the smaller policy.
func NewClientWithReadPaths(ctx context.Context, root, executable string, environment, readPaths []string) (*Client, error) {
	if executable == "" {
		executable = os.Getenv("TIANQIONG_AI_TOOL_RUNNER")
	}
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return nil, err
		}
	}
	if !filepath.IsAbs(root) || root == "/" {
		return nil, errors.New("invalid local tool workspace")
	}
	if err := os.MkdirAll(filepath.Join(root, ".tmp"), 0700); err != nil {
		return nil, err
	}
	if err := checkWorkspaceQuota(root, workspaceQuota()); err != nil {
		return nil, err
	}
	args := []string{"ai-tool-worker", "--root", root}
	if len(readPaths) > 0 {
		raw, err := json.Marshal(readPaths)
		if err != nil {
			return nil, err
		}
		args = append(args, "--read-paths", string(raw))
	}
	command := exec.Command(executable, args...)
	command.Dir = root
	command.Env = append([]string{"PATH=/usr/local/bin:/usr/bin:/bin", "NODE_PATH=/usr/local/lib/skyshield-ai-node/node_modules", "SKYSHIELD_AI_SANDBOX=1", "GOMAXPROCS=2", "HOME=" + root, "LANG=C.UTF-8", "TMPDIR=" + filepath.Join(root, ".tmp"), "PAGER=cat", "GIT_PAGER=cat", "SYSTEMD_PAGER=cat", "DEBIAN_FRONTEND=noninteractive"}, environment...)
	command.Env = append(command.Env, "NORMA_TASK_OUTPUT_ROOT="+filepath.Join(root, ".task-output"))
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	stderr := &limitedErrorWriter{}
	command.Stderr = stderr
	releaseResources, err := constrainWorker(command)
	if err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	if err = command.Start(); err != nil {
		releaseResources()
		input.Close()
		return nil, err
	}
	client := &Client{command: command, input: input, encoder: json.NewEncoder(input), writeGate: make(chan struct{}, 1), done: make(chan struct{}), transportDone: make(chan struct{}), responseTimeout: workerToolTimeout + 30*time.Second}
	client.releaseResources = releaseResources
	client.toolSlots = make(chan struct{}, 8)
	client.controlSlots = make(chan struct{}, 2)
	ready := make(chan Response, 1)
	go func() {
		defer func() { close(client.transportDone); go client.Close() }()
		decoder := json.NewDecoder(output)
		for {
			var response Response
			if err := decoder.Decode(&response); err != nil {
				return
			}
			if response.Ready {
				select {
				case ready <- response:
				default:
				}
				continue
			}
			if pending, ok := client.pending.Load(response.ID); ok {
				call := pending.(*pendingCall)
				if response.Progress != "" {
					select {
					case call.progress <- response.Progress:
					default:
					}
					continue
				}
				select {
				case call.result <- response:
				default:
				}
			}
		}
	}()
	go func() { _ = command.Wait(); close(client.done) }()
	select {
	case <-ready:
	case <-client.done:
		client.Close()
		return nil, fmt.Errorf("启动本地工具执行器失败：%s", stderr.String())
	case <-client.transportDone:
		client.Close()
		return nil, ErrTransportLost
	case <-ctx.Done():
		client.Close()
		return nil, ctx.Err()
	case <-time.After(15 * time.Second):
		client.Close()
		return nil, errors.New("本地工具执行器启动超时")
	}
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if checkWorkspaceQuota(root, workspaceQuota()) != nil {
					client.Close()
					return
				}
			case <-ctx.Done():
				client.Close()
				return
			case <-client.done:
				return
			}
		}
	}()
	return client, nil
}

func workspaceQuota() int64 {
	if value, err := strconv.ParseInt(os.Getenv("TIANQIONG_AI_WORKSPACE_BYTES"), 10, 64); err == nil && value > 0 {
		return value
	}
	return 1 << 30
}

func checkWorkspaceQuota(root string, limit int64) error {
	owned, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer owned.Close()
	maximum := 100000
	if value, err := strconv.Atoi(os.Getenv("TIANQIONG_AI_WORKSPACE_FILES")); err == nil && value > 0 {
		maximum = min(value, 1000000)
	}
	queue := []string{"."}
	var size int64
	count := 0
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		dir, err := owned.Open(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for {
			entries, readErr := dir.ReadDir(256)
			for _, entry := range entries {
				count++
				if count > maximum {
					dir.Close()
					return errors.New("AI 工作区文件数量已超过限制，请归档已有产物后继续")
				}
				if entry.IsDir() {
					queue = append(queue, filepath.Join(path, entry.Name()))
					continue
				}
				if !entry.Type().IsRegular() {
					continue
				}
				info, err := entry.Info()
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					dir.Close()
					return err
				}
				size += info.Size()
				if size > limit {
					dir.Close()
					return errors.New("AI 工作区容量已超过限制，请归档已有产物后继续")
				}
			}
			if readErr != nil {
				dir.Close()
				if errors.Is(readErr, io.EOF) {
					break
				}
				return readErr
			}
		}
	}
	return nil
}

func (c *Client) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		if c.releaseResources != nil {
			defer c.releaseResources()
		}
		_ = c.input.Close()
		_ = c.command.Process.Signal(os.Interrupt)
		select {
		case <-c.done:
		case <-time.After(2 * time.Second):
			_ = c.command.Process.Kill()
			<-c.done
		}
	})
}
func (c *Client) send(ctx context.Context, request Request) error {
	select {
	case c.writeGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrTransportLost
	}
	defer func() { <-c.writeGate }()
	// Pipe writes are part of the deadline. Closing a partial transport is safer
	// than allowing a timed-out frame to complete and execute later.
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = c.input.Close(); close(stopped) })
	err := c.encoder.Encode(request)
	if !stop() {
		<-stopped
	}
	if err != nil || ctx.Err() != nil {
		go c.Close()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
func (c *Client) Call(ctx context.Context, name string, input json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
	if ctx.Err() != nil {
		return tool.Result{}, ctx.Err()
	}
	select {
	case <-c.done:
		return tool.Result{}, ErrTransportLost
	case <-c.transportDone:
		return tool.Result{}, ErrTransportLost
	default:
	}
	id := uuid.NewString()
	call := &pendingCall{result: make(chan Response, 1), progress: make(chan string, 1)}
	response := call.result
	c.pending.Store(id, call)
	defer c.pending.Delete(id)
	request := Request{ID: id, Name: name, Input: input}
	if tc != nil {
		request.WorkingDir = tc.WorkingDir
		request.OutputDir = tc.OutputDir
		request.MaxOutputChars = tc.MaxOutputChars
		request.ToolUseID = tc.ToolUseID
		request.AgentID = tc.AgentID
	}
	timeout := c.responseTimeout
	if timeout <= 0 {
		timeout = workerToolTimeout + 30*time.Second
	}
	callCtx, stopCall := context.WithTimeout(ctx, timeout)
	defer stopCall()
	slots := c.toolSlots
	if name == "TaskStop" || name == "TaskList" || name == "TaskOutput" {
		slots = c.controlSlots
	}
	select {
	case slots <- struct{}{}:
	case <-callCtx.Done():
		return tool.Result{}, callCtx.Err()
	case <-c.done:
		return tool.Result{}, ErrTransportLost
	}
	defer func() { <-slots }()
	if err := c.send(callCtx, request); err != nil {
		if ctx.Err() != nil {
			return tool.Result{}, ctx.Err()
		}
		if callCtx.Err() != nil {
			return tool.Result{}, ErrResponseTimeout
		}
		return tool.Result{}, ErrTransportLost
	}
	for {
		select {
		case message := <-call.progress:
			if tc != nil && tc.Emit != nil {
				tc.Emit(tool.ProgressInfo{Message: message})
			}
		case value := <-response:
			return localToolResult(value)
		case <-ctx.Done():
			go func() {
				sendCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = c.send(sendCtx, Request{Cancel: id})
			}()
			return tool.Result{}, ctx.Err()
		case <-c.done:
			return interruptedLocalTool(ctx, response)
		case <-c.transportDone:
			return interruptedLocalTool(ctx, response)
		case <-callCtx.Done():
			if ctx.Err() != nil {
				return tool.Result{}, ctx.Err()
			}
			select {
			case value := <-response:
				return localToolResult(value)
			default:
			}
			go c.Close()
			return tool.Result{}, ErrResponseTimeout
		}
	}
}

func localToolResult(value Response) (tool.Result, error) {
	if value.Error != "" {
		return tool.Errorf(value.Error), nil
	}
	for _, notice := range value.Notifications {
		blob, _ := json.Marshal(notice)
		value.Result.Extra = append(value.Result.Extra, llm.UserText("[后台任务通知]\n"+string(blob)))
	}
	return value.Result, nil
}

func interruptedLocalTool(ctx context.Context, response <-chan Response) (tool.Result, error) {
	if ctx.Err() != nil {
		return tool.Result{}, ctx.Err()
	}
	// A valid final result may have arrived just before the transport closed.
	select {
	case value := <-response:
		return localToolResult(value)
	default:
	}
	return tool.Result{}, ErrTransportLost
}

type localTool struct {
	tool.CoreTool
	client *Client
}

func (t *localTool) Call(ctx context.Context, input json.RawMessage, tc *tool.ToolContext) (tool.Result, error) {
	return t.client.Call(ctx, t.Name(), input, tc)
}
func (c *Client) Tools(interactive bool) []tool.CoreTool {
	tools := NativeTools(interactive)
	for i, item := range tools {
		tools[i] = &localTool{CoreTool: item, client: c}
	}
	return tools
}

type limitedErrorWriter struct {
	mu   sync.Mutex
	text strings.Builder
}

func (w *limitedErrorWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.text.Len() < 4096 {
		_, _ = w.text.Write(value[:min(len(value), 4096-w.text.Len())])
	}
	return len(value), nil
}
func (w *limitedErrorWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.text.String()
}
