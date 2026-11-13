package handler

import (
	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

func (h *TaskHandler) SetGlobalTraffic(facade *aiapp.GlobalTrafficFacade) { h.globalTraffic = facade }
func (h *TaskHandler) GlobalTraffic(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	if h.globalTraffic == nil {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	filter, ok := trafficFilter(c)
	if !ok {
		return
	}
	result, err := h.globalTraffic.List(c.Request.Context(), user, filter)
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, result)
}
func (h *TaskHandler) GlobalTrafficHosts(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	if h.globalTraffic == nil {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	hosts, err := h.globalTraffic.Hosts(c.Request.Context(), user)
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, gin.H{"hosts": hosts})
}
