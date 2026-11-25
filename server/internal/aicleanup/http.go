package aicleanup

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

func (s *Service) Register(routes *gin.RouterGroup) {
	routes.DELETE("/aiTasks/:task", func(c *gin.Context) {
		s.request(c, func(user int) (Result, error) {
			return s.DeleteScope(c.Request.Context(), user, "task", c.Param("task"))
		})
	})
	for _, item := range []struct{ path, kind string }{{"/aiToolExecutions", "conversation"}, {"/aiTaskToolExecutions", "task"}} {
		kind := item.kind
		routes.DELETE(item.path, func(c *gin.Context) {
			var input struct {
				IDs []string `json:"ids"`
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
			if !httpdto.BindJSON(c, &input) {
				return
			}
			s.request(c, func(user int) (Result, error) { return s.DeleteExecutions(c.Request.Context(), user, kind, input.IDs) })
		})
	}
	for _, item := range []struct{ path, kind, param string }{{"/aiTasks/:task/traffic", "task", "task"}, {"/aiConversations/:conversation/traffic", "conversation", "conversation"}} {
		kind, param := item.kind, item.param
		routes.DELETE(item.path, func(c *gin.Context) {
			var input struct {
				IDs []string `json:"ids"`
				All bool     `json:"all"`
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
			if !httpdto.BindJSON(c, &input) {
				return
			}
			s.request(c, func(user int) (Result, error) {
				return s.DeleteTraffic(c.Request.Context(), user, kind, c.Param(param), input.IDs, input.All)
			})
		})
	}
}
func (s *Service) request(c *gin.Context, run func(int) (Result, error)) {
	user, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
		return
	}
	result, err := run(user)
	if err != nil {
		switch {
		case errors.Is(err, ErrForbidden):
			httpdto.Forbidden(c, err.Error())
		case errors.Is(err, ErrNotFound):
			httpdto.NotFound(c, err.Error())
		case errors.Is(err, ErrInvalid):
			httpdto.BadRequest(c, err.Error())
		case errors.Is(err, ErrBusy), errors.Is(err, ErrDependency):
			httpdto.Conflict(c, err.Error())
		default:
			httpdto.InternalError(c, "AI 数据删除未完成，请稍后重试")
		}
		return
	}
	httpdto.Success(c, result)
}
