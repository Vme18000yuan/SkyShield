package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type taskFindingApplication interface {
	Findings(context.Context, int, int64, aiapp.TaskFindingFilter, bool) (aiapp.TaskFindingPage, error)
	SetFindingStatus(context.Context, int, int64, int64, string) error
}

func (h *TaskHandler) Findings(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	app, ok := h.app.(taskFindingApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	page, ok := trafficInt(c, "page", 0)
	if !ok {
		return
	}
	size, ok := trafficInt(c, "size", 25)
	if !ok {
		return
	}
	if page < 0 || page > 10000 || size < 1 || size > 100 {
		taskError(c, aiapp.ErrInvalidInput)
		return
	}
	filter := aiapp.TaskFindingFilter{Page: int(page), Size: int(size), Query: strings.TrimSpace(c.Query("q")), Severity: strings.TrimSpace(c.Query("severity")), OwnOnly: c.Query("own") == "1"}
	if raw := c.Query("own"); raw != "" && raw != "0" && raw != "1" {
		taskError(c, aiapp.ErrInvalidInput)
		return
	}
	detail := c.Param("finding") != ""
	if detail {
		id, ok := parseCatalogID(c, "finding")
		if !ok {
			return
		}
		filter.ID = id
		filter.Page = 0
		filter.Size = 1
	}
	result, err := app.Findings(c.Request.Context(), user, task, filter, detail)
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	if detail {
		if len(result.Items) != 1 {
			taskError(c, aiapp.ErrTaskNotFound)
			return
		}
		httpdto.Success(c, result.Items[0])
		return
	}
	httpdto.Success(c, result)
}
func (h *TaskHandler) FindingStatus(c *gin.Context) {
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
	app, ok := h.app.(taskFindingApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input struct {
		Status string `json:"status"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if err := app.SetFindingStatus(c.Request.Context(), user, task, finding, input.Status); err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"updated": true})
}
