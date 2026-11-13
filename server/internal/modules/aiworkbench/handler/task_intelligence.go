package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

func (h *TaskHandler) Intelligence(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	if h.intelligence == nil {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	filter := aiapp.IntelligenceFilter{Kind: c.Query("kind"), Status: c.Query("status"), Query: c.Query("q")}
	var err error
	if raw := c.Query("limit"); raw != "" {
		filter.Limit, err = strconv.Atoi(raw)
		if err != nil {
			httpdto.BadRequest(c, "Invalid limit")
			return
		}
	}
	if raw := c.Query("offset"); raw != "" {
		filter.Offset, err = strconv.Atoi(raw)
		if err != nil {
			httpdto.BadRequest(c, "Invalid offset")
			return
		}
	}
	page, err := h.intelligence.List(c.Request.Context(), user, task, filter)
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, page)
}

func (h *TaskHandler) IntelligenceDetail(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	id, ok := parseCatalogID(c, "intelligence")
	if !ok {
		return
	}
	if h.intelligence == nil {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	page, err := h.intelligence.List(c.Request.Context(), user, task, aiapp.IntelligenceFilter{ID: id, Limit: 1})
	if err != nil {
		taskError(c, err)
		return
	}
	if len(page.Items) == 0 {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, page.Items[0])
}

func (h *TaskHandler) UpdateIntelligence(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	id, ok := parseCatalogID(c, "intelligence")
	if !ok {
		return
	}
	if h.intelligence == nil {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4*1024)
	var input struct {
		Status string `json:"status"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if err := h.intelligence.UpdateStatus(c.Request.Context(), user, task, id, input.Status); err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, gin.H{"updated": true})
}

func (h *TaskHandler) RetryCredential(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	id, ok := parseCatalogID(c, "intelligence")
	if !ok {
		return
	}
	if h.credentials == nil {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	if err := h.credentials.Retry(c.Request.Context(), user, task, id); err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusAccepted, gin.H{"queued": true})
}

func (h *TaskHandler) SetCredentialTraffic(facade *aiapp.CredentialTrafficDetailFacade) {
	h.credentialTraffic = facade
}
func (h *TaskHandler) CredentialTraffic(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	intel, ok := parseCatalogID(c, "intelligence")
	if !ok {
		return
	}
	if h.credentialTraffic == nil {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	out, err := h.credentialTraffic.Detail(c.Request.Context(), user, task, intel, c.Param("traffic"))
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, out)
}
