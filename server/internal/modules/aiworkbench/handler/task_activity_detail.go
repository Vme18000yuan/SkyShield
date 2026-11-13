package handler

import (
	"context"
	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
	"strconv"
)

func (h *TaskHandler) ActivityDetail(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("activity"), 10, 64)
	if err != nil || id <= 0 {
		httpdto.BadRequest(c, "Invalid activity ID")
		return
	}
	app, ok := h.app.(interface {
		ActivityDetail(context.Context, int, int64, int64) (string, error)
	})
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	detail, err := app.ActivityDetail(c.Request.Context(), user, task, id)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"detail": detail})
}
