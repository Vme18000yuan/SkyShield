package handler

import (
	"context"
	"strconv"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type intentRerunApplication interface {
	RerunIntent(context.Context, int, int64, int64) (int64, error)
}

// RerunIntent reopens one failed intent or every blocked intent in a task.
// POST /v1/aiTasks/:task/intents/:intent/rerun
// POST /v1/aiTasks/:task/intents/rerun-blocked
func (h *TaskHandler) RerunIntent(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	var intent int64
	if raw := c.Param("intent"); raw != "" {
		var err error
		intent, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || intent <= 0 {
			httpdto.BadRequest(c, "Invalid intent ID")
			return
		}
	}
	app, ok := h.app.(intentRerunApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	count, err := app.RerunIntent(c.Request.Context(), user, task, intent)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"reopened": count})
}
