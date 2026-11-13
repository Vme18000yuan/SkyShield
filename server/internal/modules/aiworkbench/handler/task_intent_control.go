package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type intentControlApplication interface {
	ControlIntent(context.Context, int, int64, int64, string) (string, error)
}
type intentCancelApplication interface {
	CancelIntent(context.Context, int, int64, int64, string, string) (string, error)
}

// POST /v1/aiTasks/:task/intents/:intent/control
func (h *TaskHandler) ControlIntent(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	intent, err := strconv.ParseInt(c.Param("intent"), 10, 64)
	if err != nil || intent <= 0 {
		httpdto.BadRequest(c, "Invalid intent ID")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4*1024)
	var input struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
		Mode   string `json:"mode"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	app, ok := h.app.(intentControlApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	var state string
	if input.Action == "cancel" {
		cancelApp, ok := h.app.(intentCancelApplication)
		if !ok {
			taskError(c, aiapp.ErrTaskNotFound)
			return
		}
		state, err = cancelApp.CancelIntent(c.Request.Context(), user, task, intent, input.Reason, input.Mode)
	} else {
		state, err = app.ControlIntent(c.Request.Context(), user, task, intent, input.Action)
	}
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"state": state})
}
