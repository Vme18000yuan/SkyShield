package noaadapter

import (
	"errors"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

func TestProviderAnchorIncludesNewToolOutput(t *testing.T) {
	sess := simSession(t, 200000)
	c := &Compactor{sess: sess}
	c.SetRequestBudget(12000, 8192)
	history := []llm.Message{userText("continue the authorized scan")}
	c.View(t.Context(), history)
	c.Pre(t.Context(), history, 50000)
	history = append(history, assistantWith(toolUse("scan", "probe", `{}`)), toolResult("scan", strings.Repeat("result ", 4000)))
	c.View(t.Context(), history)
	if got := sess.tokensBefore(); got <= 55000 {
		t.Fatalf("new tool output missing from provider anchor: %d", got)
	}
}

func TestRequestOverheadAndChangedModelWindow(t *testing.T) {
	sess := simSession(t, 200000)
	c := &Compactor{sess: sess}
	c.SetRequestBudget(18000, 12000)
	c.View(t.Context(), []llm.Message{userText("short")})
	if sess.tokensBefore() < 18000 {
		t.Fatal("system and tool schemas were omitted")
	}
	c.IsOverflow(errors.New("maximum context length is 64000 tokens"))
	c.overflow.arm(200000)
	sess.setEmergencyFloor(c.overflow.armedFloor())
	c.SetContextWindow(32000)
	if sess.Config().ModelContextLimit != 32000 || c.overflow.armedFloor() != 0 || sess.emergencyFloor != 0 {
		t.Fatal("model switch retained another model's window")
	}
	empty := &Compactor{sess: simSession(t, 32000)}
	if _, ok := empty.Reactive(t.Context(), nil); ok {
		t.Fatal("recovery claimed success without shrinking a rejected view")
	}
}
