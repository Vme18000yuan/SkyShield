package handler

import (
	"context"
	"strconv"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

// GET /v1/aiConversations/{conversation}/exploration
func (h *ConversationHandler) Exploration(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	if c.Query("view") == "timeline" {
		from, err := strconv.Atoi(c.DefaultQuery("from", "1"))
		after, findingErr := strconv.ParseInt(c.DefaultQuery("afterFinding", "0"), 10, 64)
		app, available := h.app.(interface {
			Timeline(context.Context, int, string, string, string, int, int64) (*aiapp.ConversationTimeline, error)
		})
		if err != nil || findingErr != nil || !available {
			conversationError(c, aiapp.ErrInvalidInput)
			return
		}
		result, err := app.Timeline(c.Request.Context(), user, c.Param("conversation"), c.Query("run"), c.Query("beforeRun"), from, after)
		if err != nil {
			conversationError(c, err)
			return
		}
		httpdto.Success(c, result)
		return
	}
	before, err := strconv.ParseInt(c.DefaultQuery("before", "0"), 10, 64)
	if err != nil || before < 0 {
		conversationError(c, aiapp.ErrInvalidInput)
		return
	}
	app, ok := h.app.(interface {
		Exploration(context.Context, int, string, string, string, int64) (*aiapp.ConversationExploration, error)
	})
	if !ok {
		conversationError(c, aiapp.ErrInvalidInput)
		return
	}
	graph, err := app.Exploration(c.Request.Context(), user, c.Param("conversation"), c.Query("run"), c.Query("beforeRun"), before)
	if err != nil {
		conversationError(c, err)
		return
	}
	httpdto.Success(c, graph)
}
