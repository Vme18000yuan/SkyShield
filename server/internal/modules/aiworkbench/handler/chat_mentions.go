package handler

import (
	"context"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

func (h *ConversationHandler) ChatMentions(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	app, ok := h.app.(interface {
		SearchChatMentions(context.Context, int, string, string, string) (aiapp.ChatMentionPage, error)
	})
	if !ok {
		conversationError(c, aiapp.ErrInvalidInput)
		return
	}
	result, err := app.SearchChatMentions(c.Request.Context(), user, c.Query("kind"), c.Query("q"), c.Query("cursor"))
	if err != nil {
		conversationError(c, err)
		return
	}
	httpdto.Success(c, result)
}
