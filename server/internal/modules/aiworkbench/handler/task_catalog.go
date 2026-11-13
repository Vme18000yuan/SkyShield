package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type taskCatalogApplication interface {
	Categories(context.Context, int) ([]aiapp.TaskCategory, error)
	SaveCategory(context.Context, int, aiapp.TaskCategory, bool) (aiapp.TaskCategory, error)
	DeleteCategory(context.Context, int, int64) error
	Templates(context.Context, int) ([]aiapp.TaskTemplate, error)
	SaveTemplate(context.Context, int, aiapp.TaskTemplate, bool) (aiapp.TaskTemplate, error)
	DeleteTemplate(context.Context, int, int64) error
}

func parseCatalogID(c *gin.Context, key string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(key), 10, 64)
	if err != nil || id <= 0 {
		httpdto.BadRequest(c, "Invalid ID")
		return 0, false
	}
	return id, true
}

func (h *TaskHandler) Categories(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	app, ok := h.app.(taskCatalogApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	if c.Request.Method == http.MethodGet {
		items, err := app.Categories(c.Request.Context(), user)
		if err != nil {
			taskError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"categories": items})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8*1024)
	var item aiapp.TaskCategory
	if !httpdto.BindJSON(c, &item) {
		return
	}
	saved, err := app.SaveCategory(c.Request.Context(), user, item, true)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, saved)
}
func (h *TaskHandler) Category(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := parseCatalogID(c, "category")
	if !ok {
		return
	}
	app, ok := h.app.(taskCatalogApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	if c.Request.Method == http.MethodDelete {
		if err := app.DeleteCategory(c.Request.Context(), user, id); err != nil {
			taskError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"ok": true})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8*1024)
	var item aiapp.TaskCategory
	if !httpdto.BindJSON(c, &item) {
		return
	}
	item.ID = id
	saved, err := app.SaveCategory(c.Request.Context(), user, item, false)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, saved)
}

func (h *TaskHandler) Templates(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	app, ok := h.app.(taskCatalogApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	if c.Request.Method == http.MethodGet {
		items, err := app.Templates(c.Request.Context(), user)
		if err != nil {
			taskError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"templates": items})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512*1024)
	var item aiapp.TaskTemplate
	if !httpdto.BindJSON(c, &item) {
		return
	}
	saved, err := app.SaveTemplate(c.Request.Context(), user, item, true)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, saved)
}
func (h *TaskHandler) Template(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := parseCatalogID(c, "template")
	if !ok {
		return
	}
	app, ok := h.app.(taskCatalogApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	if c.Request.Method == http.MethodDelete {
		if err := app.DeleteTemplate(c.Request.Context(), user, id); err != nil {
			taskError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"ok": true})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512*1024)
	var item aiapp.TaskTemplate
	if !httpdto.BindJSON(c, &item) {
		return
	}
	item.ID = id
	saved, err := app.SaveTemplate(c.Request.Context(), user, item, false)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, saved)
}
