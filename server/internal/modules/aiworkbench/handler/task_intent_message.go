package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type intentMessageApplication interface {
	SendWorkerMessage(context.Context, int, int64, int64, string, string) error
}

// POST /v1/aiTasks/:task/intents/:intent/messages
func (h *TaskHandler) SendWorkerMessage(c *gin.Context) {
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
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var input struct {
		RequestID string `json:"request_id"`
		Message   string `json:"message"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	app, ok := h.app.(intentMessageApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	if err = app.SendWorkerMessage(c.Request.Context(), user, task, intent, input.RequestID, input.Message); err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"accepted": true, "request_id": input.RequestID})
}
