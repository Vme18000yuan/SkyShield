package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type goalApplication interface {
	Goals(context.Context, int, int64) ([]aiapp.TaskGoal, error)
	SaveGoal(context.Context, int, int64, int64, string, string) (*aiapp.TaskGoal, error)
	DeleteGoal(context.Context, int, int64, int64) error
}

func (h *TaskHandler) Goals(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	app, ok := h.app.(goalApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	var id int64
	if raw := c.Param("goal"); raw != "" {
		var err error
		id, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			httpdto.BadRequest(c, "Invalid goal ID")
			return
		}
	}
	switch c.Request.Method {
	case http.MethodGet:
		items, err := app.Goals(c.Request.Context(), user, task)
		if err != nil {
			taskError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"goals": items})
	case http.MethodPost, http.MethodPut:
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32*1024)
		var input struct {
			Text      string `json:"text"`
			VulnClass string `json:"vulnclass"`
		}
		if !httpdto.BindJSON(c, &input) {
			return
		}
		item, err := app.SaveGoal(c.Request.Context(), user, task, id, input.Text, input.VulnClass)
		if err != nil {
			taskError(c, err)
			return
		}
		httpdto.Success(c, item)
	case http.MethodDelete:
		if err := app.DeleteGoal(c.Request.Context(), user, task, id); err != nil {
			taskError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"ok": true})
	}
}
