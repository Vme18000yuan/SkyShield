package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type taskScopeApplication interface {
	Scopes(context.Context, int, int64) ([]aiapp.TaskScope, error)
	AddScope(context.Context, int, int64, aiapp.TaskScopeInput) (aiapp.TaskScope, error)
	DeleteScope(context.Context, int, int64, int64) error
	Coverage(context.Context, int, int64) (aiapp.TaskCoverage, error)
}

func (h *TaskHandler) Coverage(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	app, ok := h.app.(taskScopeApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	coverage, err := app.Coverage(c.Request.Context(), user, task)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, coverage)
}

func (h *TaskHandler) Scopes(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	app, ok := h.app.(taskScopeApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	if c.Request.Method == http.MethodGet {
		items, err := app.Scopes(c.Request.Context(), user, task)
		if err != nil {
			taskError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"scope": items})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32*1024)
	var input aiapp.TaskScopeInput
	if !httpdto.BindJSON(c, &input) {
		return
	}
	item, err := app.AddScope(c.Request.Context(), user, task, input)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, item)
}

func (h *TaskHandler) DeleteScope(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	id, ok := parseCatalogID(c, "scope")
	if !ok {
		return
	}
	app, ok := h.app.(taskScopeApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	if err := app.DeleteScope(c.Request.Context(), user, task, id); err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"ok": true})
}
