package handler

import (
	"context"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type findingDuplicateApplication interface {
	FindingDuplicates(context.Context, int, int64, int64, int, int) (aiapp.FindingDuplicatePage, error)
}

func (h *TaskHandler) FindingDuplicates(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	finding, ok := parseCatalogID(c, "finding")
	if !ok {
		return
	}
	page, ok := trafficInt(c, "page", 0)
	if !ok {
		return
	}
	size, ok := trafficInt(c, "size", 20)
	if !ok {
		return
	}
	if page < 0 || page > 10000 || size < 1 || size > 100 {
		taskError(c, aiapp.ErrInvalidInput)
		return
	}
	app, ok := h.app.(findingDuplicateApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	result, err := app.FindingDuplicates(c.Request.Context(), user, task, finding, int(page), int(size))
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, result)
}
