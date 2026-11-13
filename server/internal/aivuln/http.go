package aivuln

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
	"strconv"
)

func (s *Service) RegisterRoutes(routes *gin.RouterGroup) {
	routes.GET("/aiVulnerabilityEvidence/:vulnerability", func(c *gin.Context) {
		user, ok := middleware.GetUserID(c)
		if !ok {
			httpdto.Unauthorized(c, "Not authenticated")
			return
		}
		id, err := strconv.Atoi(c.Param("vulnerability"))
		if err != nil || id <= 0 {
			httpdto.BadRequest(c, "漏洞 ID 无效")
			return
		}
		items, err := s.EvidenceForVulnerability(c.Request.Context(), user, id)
		if errors.Is(err, ErrNotFound) {
			httpdto.NotFound(c, "证据不存在或无权访问")
			return
		}
		if err != nil {
			httpdto.InternalError(c, "读取证据失败")
			return
		}
		c.Header("Cache-Control", "no-store")
		httpdto.Success(c, gin.H{"items": items})
	})
}
