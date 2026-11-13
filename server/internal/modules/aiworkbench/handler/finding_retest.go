package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type retestApplication interface {
	StartRetest(context.Context, int, int64, string) (*aiapp.FindingRetest, bool, error)
	Retests(context.Context, int, int64) ([]aiapp.FindingRetest, error)
	ActiveRetests(context.Context, int) ([]aiapp.FindingRetest, error)
	VulnerabilityFindings(context.Context, int, int64) ([]aiapp.RetestFinding, error)
}

func retestError(c *gin.Context, err error) {
	if errors.Is(err, aiapp.ErrRetestUnavailable) {
		httpdto.Conflict(c, err.Error())
		return
	}
	taskError(c, err)
}
func retestID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		httpdto.BadRequest(c, "Invalid finding ID")
		return 0, false
	}
	return id, true
}
func (h *TaskHandler) FindingRetests(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := retestID(c, "finding")
	if !ok {
		return
	}
	app, ok := h.app.(retestApplication)
	if !ok {
		retestError(c, aiapp.ErrRetestUnavailable)
		return
	}
	if c.Request.Method == http.MethodGet {
		items, err := app.Retests(c.Request.Context(), user, id)
		if err != nil {
			retestError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"retests": items})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32*1024)
	var request struct {
		Notes string `json:"notes"`
	}
	if !httpdto.BindJSON(c, &request) {
		return
	}
	item, created, err := app.StartRetest(c.Request.Context(), user, id, request.Notes)
	if err != nil {
		retestError(c, err)
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusAccepted
	}
	c.JSON(code, gin.H{"retest": item, "created": created})
}
func (h *TaskHandler) ActiveRetests(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	app, ok := h.app.(retestApplication)
	if !ok {
		retestError(c, aiapp.ErrRetestUnavailable)
		return
	}
	items, err := app.ActiveRetests(c.Request.Context(), user)
	if err != nil {
		retestError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"retests": items})
}
func (h *TaskHandler) VulnerabilityFindings(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := retestID(c, "vulnerability")
	if !ok {
		return
	}
	app, ok := h.app.(retestApplication)
	if !ok {
		retestError(c, aiapp.ErrRetestUnavailable)
		return
	}
	items, err := app.VulnerabilityFindings(c.Request.Context(), user, id)
	if err != nil {
		retestError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"findings": items})
}
