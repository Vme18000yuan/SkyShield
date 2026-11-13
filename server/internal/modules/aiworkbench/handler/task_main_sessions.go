package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type taskMainSessionApplication interface {
	MainSessions(context.Context, int, int64) ([]aiapp.TaskMainSession, error)
	CreateMainSession(context.Context, int, int64) (aiapp.TaskMainSession, error)
	MainMessages(context.Context, int, int64, int, int64) ([]aiapp.TaskActivity, error)
}

func (h *TaskHandler) MainMessages(c *gin.Context) {
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
	app, ok := h.app.(taskMainSessionApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	items, err := app.MainMessages(c.Request.Context(), user, task, segment, before)
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
		out = append(out, gin.H{"id": strconv.FormatInt(item.ID, 10), "worker": item.Worker, "kind": item.Kind, "summary": item.Summary, "detail": item.Detail, "isError": item.IsError, "createdAt": item.CreatedAt.UTC(), "mainSeg": segment})
	}
	httpdto.Success(c, gin.H{"messages": out, "nextBefore": next})
}

func (h *TaskHandler) MainSessions(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	app, ok := h.app.(taskMainSessionApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	if c.Request.Method == http.MethodGet {
		items, err := app.MainSessions(c.Request.Context(), user, task)
		if err != nil {
			taskError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"sessions": items, "current": items[0].Seq})
		return
	}
	item, err := app.CreateMainSession(c.Request.Context(), user, task)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, item)
}
