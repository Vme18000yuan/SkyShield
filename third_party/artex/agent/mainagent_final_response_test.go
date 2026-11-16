package agent

import (
	"context"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/tool"
)

func TestTaskMainChatRecoversReasoningOnlyCompletion(t *testing.T) {
	for _, syncMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "stream", true: "complete"}[syncMode], func(t *testing.T) {
			calls := 0
			provider := captureUsageProvider{stream: func(_ context.Context, yield func(llm.StreamEvent, error) bool) {
				calls++
				event := llm.StreamEvent{Type: llm.SEThinkingDelta, Text: "fixture private reasoning"}
				if calls == 2 {
					event = llm.StreamEvent{Type: llm.SETextDelta, Text: "任务现有记录已汇总，未执行新的检查。"}
				}
				for _, item := range []llm.StreamEvent{event, {Type: llm.SEMessageDelta, StopReason: "end_turn"}, {Type: llm.SEMessageStop}} {
					if !yield(item, nil) {
						return
					}
				}
			}}
			main := NewMainAgent(provider, "fixture", t.TempDir(), nil, 100000, 0)
			main.SetNonStreaming(func() bool { return syncMode })
			ctx := WithHostPolicy(context.Background(), HostPolicy{AllowWebFetch: false, TransformTools: func(context.Context, string, []tool.CoreTool) []tool.CoreTool { return []tool.CoreTool{} }})
			var final db.Activity
			reply, err := main.Chat(ctx, 17, 0, nil, (&db.DB{}).Exploration(17), "fixture goal", "summarize", func(a db.Activity) {
				if a.Kind == "result" {
					final = a
				}
			}, nil, nil, nil, nil)
			if err != nil || calls != 2 || reply != "任务现有记录已汇总，未执行新的检查。" || final.TerminalReason != "completed" || final.Detail != reply {
				t.Fatalf("task main chat: reply=%q err=%v calls=%d final=%+v", reply, err, calls, final)
			}
		})
	}
}
