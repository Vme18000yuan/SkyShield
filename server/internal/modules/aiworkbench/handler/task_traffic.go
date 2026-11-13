package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

func (h *TaskHandler) SetTraffic(facade *aiapp.TaskTrafficFacade) { h.traffic = facade }
func trafficInt(c *gin.Context, key string, fallback int64) (int64, bool) {
	raw := c.Query(key)
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		httpdto.BadRequest(c, "Invalid traffic pagination")
		return 0, false
	}
	return value, true
}
func trafficFilter(c *gin.Context) (aiapp.TaskTrafficFilter, bool) {
	page, ok := trafficInt(c, "page", 0)
	if !ok {
		return aiapp.TaskTrafficFilter{}, false
	}
	size, ok := trafficInt(c, "size", 25)
	if !ok {
		return aiapp.TaskTrafficFilter{}, false
	}
	if page < 0 || page > 10000 || size < 1 || size > 100 {
		taskError(c, aiapp.ErrInvalidInput)
		return aiapp.TaskTrafficFilter{}, false
	}
	filter := aiapp.TaskTrafficFilter{Page: int(page), Size: int(size), Host: c.Query("host"), Method: c.Query("method"), Query: c.Query("q"), Body: c.Query("body"), Path: c.Query("path"), Status: c.Query("status"), Sort: c.Query("sort"), Order: c.Query("order")}
	for _, field := range []struct {
		name string
		dest **int64
	}{{"respMin", &filter.RespMin}, {"respMax", &filter.RespMax}} {
		if c.Query(field.name) == "" {
			continue
		}
		value, valid := trafficInt(c, field.name, 0)
		if !valid {
			return aiapp.TaskTrafficFilter{}, false
		}
		*field.dest = &value
	}
	return filter, true
}
func (h *TaskHandler) Traffic(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	if h.traffic == nil {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	filter, ok := trafficFilter(c)
	if !ok {
		return
	}
	result, err := h.traffic.List(c.Request.Context(), user, task, filter)
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, result)
}
func (h *TaskHandler) TrafficExchange(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	if h.traffic == nil {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	request, response, err := h.traffic.Exchange(c.Request.Context(), user, task, strings.TrimSpace(c.Query("id")))
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, gin.H{"request": request, "response": response})
}
func (h *TaskHandler) TrafficBlob(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	task, ok := taskID(c)
	if !ok {
		return
	}
	if h.traffic == nil {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	offset, ok := trafficInt(c, "offset", 0)
	if !ok {
		return
	}
	length, ok := trafficInt(c, "length", 8192)
	if !ok {
		return
	}
	body, total, err := h.traffic.Blob(c.Request.Context(), user, task, strings.TrimSpace(c.Query("hash")), offset, length)
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"data": body, "offset": offset, "total": total})
}
