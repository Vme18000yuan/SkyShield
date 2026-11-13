package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type taskMainChatApplication interface {
	StartMainChat(context.Context, int, int64, string, *int, []aiapp.ChatAttachment) error
	MainChatRunning(context.Context, int, int64) (bool, error)
	StopMainChat(context.Context, int, int64) (bool, error)
}

func (h *TaskHandler) MainChat(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := taskID(c)
	if !ok {
		return
	}
	app, ok := h.app.(taskMainChatApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskChatUnavailable)
		return
	}
	c.Header("Cache-Control", "no-store")
	if c.Request.Method == http.MethodGet {
		running, err := app.MainChatRunning(c.Request.Context(), user, id)
		if err != nil {
			taskError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"running": running})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 68*1024)
	var input struct {
		Message     string                 `json:"message"`
		Segment     *int                   `json:"segment"`
		Attachments []aiapp.ChatAttachment `json:"attachments"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if err := app.StartMainChat(c.Request.Context(), user, id, input.Message, input.Segment, input.Attachments); err != nil {
		taskError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "accepted"})
}
func (h *TaskHandler) StopMainChat(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := taskID(c)
	if !ok {
		return
	}
	app, ok := h.app.(taskMainChatApplication)
	if !ok {
		taskError(c, aiapp.ErrTaskChatUnavailable)
		return
	}
	stopping, err := app.StopMainChat(c.Request.Context(), user, id)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"stopping": stopping})
}
