package airuntime

import (
	"context"
	"errors"
	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/tool"
	"github.com/yyhuni/lunafox/server/internal/ailocal"
	"github.com/yyhuni/lunafox/server/internal/httpoutbound"
	"os"
	"path/filepath"
	"strings"
)

// NativeLocalTools keeps the original Norma file, Bash, background-task and PTY
// handlers. Only their execution boundary changes for the hosted ASM product.
type NativeLocalTools struct {
	client  *ailocal.Client
	relay   *httpoutbound.LocalRelay
	release func()
}

func NewNativeLocalTools(ctx context.Context, workspace string) (*NativeLocalTools, error) {
	return newNativeLocalToolsWithInterpreter(ctx, workspace, "")
}

// An explicitly configured virtual environment gets a separate worker with
// read-only access to its runtime directories, never the ordinary shell worker.
func newNativeLocalToolsWithInterpreter(ctx context.Context, workspace, interpreter string) (*NativeLocalTools, error) {
	ctx, release, err := AcquireToolProcess(ctx, true)
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			release()
		}
	}()
	if err := workspaceScopeError(ctx); err != nil {
		return nil, err
	}
	workspace, shared := scopedWorkspace(ctx, workspace)
	paths, err := interpreterReadPaths(interpreter)
	if err != nil {
		return nil, err
	}
	paths = append(paths, shared...)
	relay, err := httpoutbound.NewLocalRelay(ctx)
	if err != nil {
		return nil, err
	}
	if relay.CABundle() != "" {
		paths = append(paths, relay.CABundle())
	}
	client, err := ailocal.NewClientWithReadPaths(ctx, workspace, "", relay.Environment(), paths)
	if err != nil {
		relay.Close()
		return nil, err
	}
	transferred = true
	return &NativeLocalTools{client: client, relay: relay, release: release}, nil
}
func (n *NativeLocalTools) Close() {
	if n != nil {
		n.client.Close()
		n.relay.Close()
		if n.release != nil {
			n.release()
		}
	}
}
func (n *NativeLocalTools) Tools(interactive bool) []tool.CoreTool {
	tools := append(n.client.Tools(interactive), tool.NewWebFetch(tool.WebFetchConfig{Proxy: n.relay.URL(), CACert: n.relay.CABundle()}))
	for i, item := range tools {
		switch item.Name() {
		case "TaskOutput", "TaskList", "TaskStop", "Monitor":
			tools[i] = agent.DecorateTool(item, item.Description()+"\n后台任务编号只属于当前执行器/本轮运行。不要直接复用历史对话中的旧 task_id；先用 TaskList 核对当前任务。旧运行的输出文件只有仍存在时才能用 Read 读取，不要为补输出自动重跑有副作用的命令。", item.InputSchema())
		}
	}
	if interactive {
		for i, item := range tools {
			if item.Name() == "Bash" {
				tools[i] = agent.DecorateTool(item, item.Description()+"\n需要交互输入的程序请用 shell_open，使用结束后调用 shell_close。", item.InputSchema())
			}
		}
	}
	return tools
}

type ToolLayerResolver func(context.Context, int, string, []tool.CoreTool) ([]tool.CoreTool, error)

// interpreterReadPaths permits an administrator-selected executable and, when
// it is in a Python virtual environment, only its runtime folders and config.
func interpreterReadPaths(interpreter string) ([]string, error) {
	interpreter = strings.TrimSpace(interpreter)
	if interpreter == "" {
		return nil, nil
	}
	if !filepath.IsAbs(interpreter) {
		if strings.ContainsAny(interpreter, "/\\\x00") {
			return nil, errors.New("invalid Python interpreter")
		}
		return nil, nil
	}
	interpreter = filepath.Clean(interpreter)
	info, err := os.Stat(interpreter)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return nil, errors.New("invalid Python interpreter")
	}
	paths := []string{interpreter}
	bin := filepath.Dir(interpreter)
	if filepath.Base(bin) != "bin" {
		return paths, nil
	}
	root := filepath.Dir(bin)
	if root == "/" {
		return paths, nil
	}
	config := filepath.Join(root, "pyvenv.cfg")
	info, err = os.Stat(config)
	if err != nil || !info.Mode().IsRegular() {
		return paths, nil
	}
	for _, path := range []string{bin, filepath.Join(root, "lib"), filepath.Join(root, "lib64"), config} {
		if _, err := os.Stat(path); err == nil {
			paths = append(paths, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return paths, nil
}
