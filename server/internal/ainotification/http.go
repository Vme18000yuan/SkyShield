package ainotification

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type Handler struct{ store *Store }

func NewHandler(store *Store) *Handler {
	if store == nil {
		panic("AI notification store is required")
	}
	return &Handler{store: store}
}

func (h *Handler) Register(group *gin.RouterGroup) {
	group.GET("/aiNotification/meta", h.meta)
	group.PUT("/aiNotification/settings", h.settings)
	group.PATCH("/aiNotification/settings", h.settings)
	group.GET("/aiNotification/channels", h.channels)
	group.POST("/aiNotification/channels", h.channels)
	group.PATCH("/aiNotification/channels/:channel", h.channel)
	group.DELETE("/aiNotification/channels/:channel", h.channel)
	group.POST("/aiNotification/channels/:channel/test", h.test)
	group.GET("/aiNotification/deliveries", h.deliveries)
	group.POST("/aiNotification/deliveries/:delivery/retry", h.retry)
}

func requestUser(c *gin.Context) (int, bool) {
	user, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
	}
	c.Header("Cache-Control", "no-store")
	return user, ok
}
func requestID(c *gin.Context, key string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(key), 10, 64)
	if err != nil || id <= 0 {
		httpdto.BadRequest(c, "Invalid ID")
		return 0, false
	}
	return id, true
}
func respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalid):
		httpdto.BadRequest(c, err.Error())
	case errors.Is(err, ErrNotFound):
		httpdto.NotFound(c, "AI notification resource not found")
	case errors.Is(err, ErrConflict):
		httpdto.Conflict(c, "AI notification resource changed or is unavailable")
	default:
		httpdto.InternalError(c, "AI notification request failed")
	}
}

func (h *Handler) meta(c *gin.Context) {
	user, ok := requestUser(c)
	if !ok {
		return
	}
	settings, err := h.store.Settings(c.Request.Context(), user)
	if err != nil {
		respondError(c, err)
		return
	}
	stats, err := h.store.Statistics(c.Request.Context(), user)
	if err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"settings": settings, "kinds": Kinds(), "stats": stats})
}
func (h *Handler) settings(c *gin.Context) {
	user, ok := requestUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if c.Request.Method == http.MethodPatch {
		var patch SettingsPatch
		if !httpdto.BindJSON(c, &patch) {
			return
		}
		if err := h.store.PatchSettings(c.Request.Context(), user, patch); err != nil {
			respondError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"updated": true})
		return
	}
	var in Settings
	if !httpdto.BindJSON(c, &in) {
		return
	}
	if err := h.store.SaveSettings(c.Request.Context(), user, in); err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"updated": true})
}
func (h *Handler) channels(c *gin.Context) {
	user, ok := requestUser(c)
	if !ok {
		return
	}
	if c.Request.Method == http.MethodGet {
		items, err := h.store.Channels(c.Request.Context(), user)
		if err != nil {
			respondError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"channels": items})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var in ChannelInput
	if !httpdto.BindJSON(c, &in) {
		return
	}
	item, err := h.store.SaveChannel(c.Request.Context(), user, 0, in)
	if err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, item)
}
func (h *Handler) channel(c *gin.Context) {
	user, ok := requestUser(c)
	if !ok {
		return
	}
	id, ok := requestID(c, "channel")
	if !ok {
		return
	}
	if c.Request.Method == http.MethodDelete {
		if err := h.store.DeleteChannel(c.Request.Context(), user, id); err != nil {
			respondError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"deleted": true})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var in ChannelInput
	if !httpdto.BindJSON(c, &in) {
		return
	}
	item, err := h.store.SaveChannel(c.Request.Context(), user, id, in)
	if err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, item)
}
func (h *Handler) test(c *gin.Context) {
	user, ok := requestUser(c)
	if !ok {
		return
	}
	id, ok := requestID(c, "channel")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if err := h.store.TestChannel(ctx, user, id); err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"sent": true})
}
func (h *Handler) deliveries(c *gin.Context) {
	user, ok := requestUser(c)
	if !ok {
		return
	}
	page, size := 0, 25
	var err error
	if value := c.Query("page"); value != "" {
		page, err = strconv.Atoi(value)
		if err != nil {
			respondError(c, ErrInvalid)
			return
		}
	}
	if value := c.Query("size"); value != "" {
		size, err = strconv.Atoi(value)
		if err != nil {
			respondError(c, ErrInvalid)
			return
		}
	}
	channelID := int64(0)
	if value := c.Query("channel_id"); value != "" {
		channelID, err = strconv.ParseInt(value, 10, 64)
		if err != nil || channelID <= 0 {
			respondError(c, ErrInvalid)
			return
		}
	}
	out, err := h.store.FilteredDeliveries(c.Request.Context(), user, page, size, channelID, c.Query("state"))
	if err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, out)
}
func (h *Handler) retry(c *gin.Context) {
	user, ok := requestUser(c)
	if !ok {
		return
	}
	id, ok := requestID(c, "delivery")
	if !ok {
		return
	}
	if err := h.store.RetryDelivery(c.Request.Context(), user, id); err != nil {
		respondError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"queued": true})
}
