package airuntime

import (
	"context"
	"sync"
)

// DeferredChatRunner keeps the ASM server available when AI storage has not
// yet been configured; creation errors are returned by the first AI run.
type DeferredChatRunner struct {
	root   string
	once   sync.Once
	runner *ChatRunner
	err    error
}

func NewDeferredChatRunner(root string) *DeferredChatRunner { return &DeferredChatRunner{root: root} }

func (r *DeferredChatRunner) Run(ctx context.Context, run ChatRun, emit func(Activity)) (string, error) {
	r.once.Do(func() { r.runner, r.err = NewChatRunner(r.root) })
	if r.err != nil {
		return "", r.err
	}
	return r.runner.Run(ctx, run, emit)
}
