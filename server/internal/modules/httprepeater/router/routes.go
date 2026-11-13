package router

import (
	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/modules/httprepeater/handler"
)

func Register(routes *gin.RouterGroup, h *handler.Handler) { routes.POST("/httpRepeater:send", h.Send) }
