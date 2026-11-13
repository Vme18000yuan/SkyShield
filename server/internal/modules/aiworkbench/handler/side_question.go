package handler

import (
	"encoding/json"
	"fmt"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
	"net/http"
	"strconv"
	"time"
)

func (h *ConversationHandler) SideQuestions(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	if h.side == nil {
		conversationError(c, aiapp.ErrModelNotConfigured)
		return
	}
	parent := sidequestion.Parent{ConversationKey: c.Param("conversation")}
	if c.Param("task") != "" {
		parent.TaskID, _ = strconv.ParseInt(c.Param("task"), 10, 64)
		parent.IntentID, _ = strconv.ParseInt(c.Param("intent"), 10, 64)
		if raw := c.Param("segment"); raw != "" {
			segment, err := strconv.Atoi(raw)
			if err != nil || segment < 0 {
				conversationError(c, aiapp.ErrInvalidInput)
				return
			}
			parent.MainSeg = segment
		}
		if parent.TaskID <= 0 || c.Param("intent") != "" && parent.IntentID <= 0 {
			conversationError(c, aiapp.ErrInvalidInput)
			return
		}
	}
	switch c.Request.Method {
	case http.MethodGet:
		before, err := strconv.ParseInt(c.DefaultQuery("before", "0"), 10, 64)
		if err != nil {
			conversationError(c, aiapp.ErrInvalidInput)
			return
		}
		history, err := h.side.History(c.Request.Context(), user, parent, before)
		if err != nil {
			conversationError(c, err)
			return
		}
		httpdto.Success(c, history)
	case http.MethodPost:
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 72*1024)
		var input struct {
			Question string `json:"question"`
			ClientID string `json:"client_request_id"`
		}
		if !httpdto.BindJSON(c, &input) {
			return
		}
		item, err := h.side.Ask(c.Request.Context(), user, parent, input.Question, input.ClientID)
		if err != nil {
			conversationError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, item)
	case http.MethodDelete:
		if err := h.side.Clear(c.Request.Context(), user, parent); err != nil {
			conversationError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"ok": true})
	}
}
func (h *ConversationHandler) CancelSideQuestion(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	if h.side == nil {
		conversationError(c, aiapp.ErrModelNotConfigured)
		return
	}
	if err := h.side.Cancel(c.Request.Context(), user, c.Param("question")); err != nil {
		conversationError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"ok": true})
}
func (h *ConversationHandler) SideQuestionEvents(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	if h.side == nil {
		conversationError(c, aiapp.ErrModelNotConfigured)
		return
	}
	item, err := h.side.Get(c.Request.Context(), user, c.Param("question"))
	if err != nil {
		conversationError(c, err)
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.Status(200)
	c.Writer.Flush()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	var last string
	heartbeat := time.Now()
	for {
		raw, err := json.Marshal(item)
		if err != nil {
			return
		}
		if string(raw) != last {
			fmt.Fprintf(c.Writer, "event: snapshot\ndata: %s\n\n", raw)
			c.Writer.Flush()
			last = string(raw)
		}
		if !item.Running() {
			return
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
			item, err = h.side.Get(c.Request.Context(), user, c.Param("question"))
			if err != nil {
				fmt.Fprint(c.Writer, "event: cleared\ndata: {}\n\n")
				c.Writer.Flush()
				return
			}
			if time.Since(heartbeat) > 15*time.Second {
				fmt.Fprint(c.Writer, ": heartbeat\n\n")
				c.Writer.Flush()
				heartbeat = time.Now()
			}
		}
	}
}
