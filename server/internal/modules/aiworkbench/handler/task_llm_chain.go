package handler

import (
	"context"
	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
	"net/http"
)

type taskLLMApplication interface {
	TaskModels(context.Context, int, int64) (aiapp.TaskLLMState, error)
	ReplaceTaskModels(context.Context, int, int64, []string, *string) (aiapp.TaskLLMState, error)
}

func (h *TaskHandler) Models(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := taskID(c)
	if !ok {
		return
	}
	app, ok := h.app.(taskLLMApplication)
	if !ok {
		taskError(c, aiapp.ErrInvalidInput)
		return
	}
	if c.Request.Method == http.MethodGet {
		state, err := app.TaskModels(c.Request.Context(), user, id)
		if err != nil {
			taskError(c, err)
			return
		}
		httpdto.Success(c, state)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	var input struct {
		ProfileIDs      []string `json:"profileIds"`
		ActiveProfileID *string  `json:"activeProfileId"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	state, err := app.ReplaceTaskModels(c.Request.Context(), user, id, input.ProfileIDs, input.ActiveProfileID)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, state)
}
