package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type constraintApplication interface {
	Constraints(context.Context, int, int64) ([]aiapp.TaskConstraint, error)
	SaveConstraint(context.Context, int, int64, int64, string, string) (*aiapp.TaskConstraint, error)
	DeleteConstraint(context.Context, int, int64, int64) error
}

func (h *TaskHandler) Constraints(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	app, ok := h.app.(constraintApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	var id int64
	if raw := c.Param("constraint"); raw != "" {
		var err error
		id, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			httpdto.BadRequest(c, "Invalid constraint ID")
			return
		}
	}
	switch c.Request.Method {
	case http.MethodGet:
		items, err := app.Constraints(c.Request.Context(), user, task)
		if err != nil {
			taskError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"constraints": items})
	case http.MethodPost, http.MethodPut:
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32*1024)
		var request struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		}
		if !httpdto.BindJSON(c, &request) {
			return
		}
		item, err := app.SaveConstraint(c.Request.Context(), user, task, id, request.Kind, request.Text)
		if err != nil {
			taskError(c, err)
			return
		}
		httpdto.Success(c, item)
	case http.MethodDelete:
		if err := app.DeleteConstraint(c.Request.Context(), user, task, id); err != nil {
			taskError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"ok": true})
	}
}
