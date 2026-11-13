package handler

import (
	"context"
	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
	"io"
	"net/http"
	"strconv"
)

type conversationHistoryApplication interface {
	History(context.Context, int, string, int64, int) (*aiapp.ActivityPage, error)
	ActivityDetail(context.Context, int, string, int64) (string, error)
	DeleteConversations(context.Context, int, []string) ([]aiapp.ConversationDeleteItem, error)
}

func nativeActivity(item aiapp.ConversationActivity) gin.H {
	return gin.H{"seq": item.ID, "worker": item.Worker, "kind": item.Kind, "tool": item.Tool, "tool_use_id": item.ToolUseID, "is_error": item.IsError, "summary": item.Summary, "ts": item.CreatedAt.UTC(), "input_tokens": item.InputTokens, "output_tokens": item.OutputTokens, "cache_read_tokens": item.CacheReadTokens, "cache_write_tokens": item.CacheWriteTokens}
}
func (h *ConversationHandler) Messages(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	app, ok := h.app.(conversationHistoryApplication)
	if !ok {
		conversationError(c, aiapp.ErrInvalidInput)
		return
	}
	var page *aiapp.ActivityPage
	var err error
	if c.Query("since") != "" {
		since, parseErr := strconv.ParseInt(c.Query("since"), 10, 64)
		if parseErr != nil || since < 0 {
			conversationError(c, aiapp.ErrInvalidInput)
			return
		}
		if feed, ok := h.app.(interface {
			ActivityFeed(context.Context, int, string, int64) (*aiapp.ActivityPage, error)
		}); ok {
			page, err = feed.ActivityFeed(c.Request.Context(), user, c.Param("conversation"), since)
		} else {
			page, err = h.app.Activities(c.Request.Context(), user, c.Param("conversation"), since)
		}
		if page != nil {
			page.Cursor = since
			if len(page.Items) > 0 {
				page.Cursor = page.Items[len(page.Items)-1].ID
			}
		}
	} else {
		before, _ := strconv.ParseInt(c.DefaultQuery("before", "0"), 10, 64)
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
		page, err = app.History(c.Request.Context(), user, c.Param("conversation"), before, limit)
	}
	if err != nil {
		conversationError(c, err)
		return
	}
	items := make([]gin.H, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, nativeActivity(item))
	}
	httpdto.Success(c, gin.H{"items": items, "cursor": page.Cursor, "running": page.Running, "hasMore": page.HasMore,
		"finalCursor": page.FinalCursor, "runStatus": page.RunStatus, "terminalReason": page.TerminalReason,
		"errorMessage": page.ErrorMessage, "lastProgressAt": page.LastProgressAt, "liveText": page.LiveText, "liveKind": page.LiveKind})
}
func (h *ConversationHandler) MessageDetail(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(c.Param("activity"), 10, 64)
	app, ok := h.app.(conversationHistoryApplication)
	if !ok {
		conversationError(c, aiapp.ErrInvalidInput)
		return
	}
	detail, err := app.ActivityDetail(c.Request.Context(), user, c.Param("conversation"), id)
	if err != nil {
		conversationError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"detail": detail})
}
func (h *ConversationHandler) Delete(c *gin.Context) {
	h.deleteConversations(c, []string{c.Param("conversation")})
}
func (h *ConversationHandler) BatchDelete(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	var input struct {
		IDs []string `json:"ids"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	h.deleteConversations(c, input.IDs)
}
func (h *ConversationHandler) deleteConversations(c *gin.Context, ids []string) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	app, ok := h.app.(conversationHistoryApplication)
	if !ok {
		conversationError(c, aiapp.ErrInvalidInput)
		return
	}
	items, err := app.DeleteConversations(c.Request.Context(), user, ids)
	if err != nil {
		conversationError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"items": items})
}
func (h *ConversationHandler) Upload(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	app, ok := h.app.(interface {
		UploadAttachment(context.Context, int, string, string, string, io.Reader) (aiapp.ChatAttachment, error)
	})
	if !ok {
		conversationError(c, aiapp.ErrInvalidInput)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, aiapp.MaxChatUpload)
	if err := c.Request.ParseMultipartForm(8 << 20); err != nil {
		httpdto.BadRequest(c, "附件无效或超过 128 MiB")
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	files := c.Request.MultipartForm.File["file"]
	if len(files) == 0 || len(files) > 20 {
		conversationError(c, aiapp.ErrInvalidInput)
		return
	}
	items := make([]aiapp.ChatAttachment, 0, len(files))
	for _, header := range files {
		file, err := header.Open()
		if err != nil {
			conversationError(c, err)
			return
		}
		item, err := app.UploadAttachment(c.Request.Context(), user, c.Query("scope"), c.Query("id"), header.Filename, file)
		file.Close()
		if err != nil {
			conversationError(c, err)
			return
		}
		items = append(items, item)
	}
	httpdto.Success(c, gin.H{"attachments": items})
}
