package handler

import (
	"context"
	"strconv"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

func (h *TaskHandler) MainTranscript(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	segment, err := strconv.Atoi(c.Param("segment"))
	if err != nil || segment < 0 {
		httpdto.BadRequest(c, "Invalid session segment")
		return
	}
	var before int64
	if raw := c.Query("before"); raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before <= 0 {
			httpdto.BadRequest(c, "Invalid cursor")
			return
		}
	}
	app, ok := h.app.(interface {
		MainTranscript(context.Context, int, int64, int, int64) ([]aiapp.TaskActivity, error)
	})
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	items, err := app.MainTranscript(c.Request.Context(), user, task, segment, before)
	if err != nil {
		taskError(c, err)
		return
	}
	next := ""
	if len(items) > 100 {
		items = items[:100]
		next = strconv.FormatInt(items[len(items)-1].ID, 10)
	}
	out := make([]gin.H, 0, len(items))
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		kind := item.Kind
		if kind == "human_message" {
			kind = "user"
		} else if kind == "assistant_message" {
			kind = "result"
		}
		out = append(out, gin.H{"seq": item.ID, "worker": item.Worker, "kind": kind, "tool": item.Tool, "tool_use_id": item.ToolUseID, "is_error": item.IsError, "summary": item.Summary, "ts": item.CreatedAt.UTC(), "main_seg": segment})
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, gin.H{"activities": out, "nextBefore": next})
}
