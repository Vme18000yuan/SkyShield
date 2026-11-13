package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

func (h *ConversationHandler) SetTraffic(facade *aiapp.ConversationTrafficFacade) { h.traffic = facade }

func (h *ConversationHandler) Traffic(c *gin.Context) {
	user, ok := conversationUser(c)
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
	result, err := h.traffic.List(c.Request.Context(), user, c.Param("conversation"), filter)
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, result)
}
func (h *ConversationHandler) TrafficExchange(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	if h.traffic == nil {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	request, response, err := h.traffic.Exchange(c.Request.Context(), user, c.Param("conversation"), strings.TrimSpace(c.Query("id")))
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, gin.H{"request": request, "response": response})
}
func (h *ConversationHandler) TrafficBlob(c *gin.Context) {
	user, ok := conversationUser(c)
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
	body, total, err := h.traffic.Blob(c.Request.Context(), user, c.Param("conversation"), strings.TrimSpace(c.Query("hash")), offset, length)
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"data": body, "offset": offset, "total": total})
}
