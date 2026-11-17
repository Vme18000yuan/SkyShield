// Package productupdate connects authenticated administrators to the deployment's
// private host updater. The Server never executes host commands or owns Docker.
package productupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
	"gorm.io/gorm"
)

type Handler struct {
	db           *gorm.DB
	socket, mode string
	client       *http.Client
}

func New(db *gorm.DB, mode, socket string) *Handler {
	return &Handler{db: db, mode: mode, socket: socket, client: &http.Client{
		Timeout: 45 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", socket)
		}},
	}}
}

func (h *Handler) Register(routes *gin.RouterGroup) {
	routes.GET("/deploymentUpdate", h.status)
	routes.POST("/system:checkForUpdates", h.check)
	routes.POST("/deploymentUpdate:apply", h.apply)
	// Old clients must not revive LunaFox's operation runner for a SkyShield deployment.
	routes.GET("/upgradeOperations:active", func(c *gin.Context) { httpdto.NotFound(c, "No active legacy upgrade") })
}

// Maintenance blocks new application writes during the short service handoff.
// Host-side work checks are repeated after this marker is installed.
func (h *Handler) Maintenance(c *gin.Context) {
	if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead ||
		c.Request.URL.Path == "/v1/sessions:renew" || c.Request.URL.Path == "/v1/system:checkForUpdates" {
		c.Next()
		return
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(h.socket), "maintenance")); err == nil {
		httpdto.ErrorWithStatus(c, 503, "UNAVAILABLE", "SYSTEM_UPDATING", "System update is in progress. Try again after the service restarts.")
		c.Abort()
		return
	}
	c.Next()
}

func (h *Handler) authorize(c *gin.Context) bool {
	id, ok := middleware.GetUserID(c)
	if !ok || id <= 0 {
		httpdto.Unauthorized(c, "Not authenticated")
		return false
	}
	var count int64
	err := h.db.WithContext(c.Request.Context()).Table("auth_user").Where("id = ? AND is_active = ? AND is_superuser = ?", id, true, true).Count(&count).Error
	if err != nil {
		httpdto.InternalError(c, "Unable to check update permission")
		return false
	}
	if count != 1 {
		httpdto.Forbidden(c, "Only an active system administrator can manage updates")
		return false
	}
	return true
}
func (h *Handler) status(c *gin.Context) {
	if h.authorize(c) {
		h.forward(c, "/status", nil)
	}
}
func (h *Handler) check(c *gin.Context) {
	if c.Request.URL.Path != "/v1/system:checkForUpdates" {
		httpdto.NotFound(c, "Custom method not found")
		return
	}
	if !h.authorize(c) {
		return
	}
	var empty struct{}
	if !httpdto.BindJSON(c, &empty) {
		return
	}
	h.forward(c, "/check", map[string]any{})
}
func (h *Handler) apply(c *gin.Context) {
	if c.Request.URL.Path != "/v1/deploymentUpdate:apply" {
		httpdto.NotFound(c, "Custom method not found")
		return
	}
	if !h.authorize(c) {
		return
	}
	var input struct {
		RequestID      string `json:"requestId"`
		ManifestDigest string `json:"manifestDigest"`
		Confirmed      bool   `json:"confirmed"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	id, err := uuid.Parse(input.RequestID)
	if err != nil || id.String() != input.RequestID || id == uuid.Nil || !input.Confirmed {
		httpdto.BadRequest(c, "A request UUID and explicit confirmation are required")
		return
	}
	h.forward(c, "/apply", input)
}
func (h *Handler) forward(c *gin.Context, path string, payload any) {
	method := http.MethodGet
	var body io.Reader
	if payload != nil {
		data, _ := json.Marshal(payload)
		body = bytes.NewReader(data)
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), method, "http://updater"+path, body)
	if err != nil {
		httpdto.InternalError(c, "Unable to create updater request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := h.client.Do(req)
	if err != nil {
		if path == "/status" || path == "/check" {
			httpdto.Success(c, gin.H{"currentVersion": "unknown", "mode": h.mode, "updaterReady": false, "hasUpdate": false, "eligible": false, "checkState": "updater_unavailable", "message": "宿主机更新进程未运行，请在项目目录执行 ./update.sh start --mode " + h.mode, "operation": nil})
		} else {
			httpdto.ErrorWithStatus(c, 503, "UNAVAILABLE", "UPDATER_UNAVAILABLE", "Host updater is unavailable; start it with ./update.sh start")
		}
		return
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 256*1024+1))
	if err != nil || len(raw) > 256*1024 || !json.Valid(raw) {
		httpdto.InternalError(c, "Invalid host updater response")
		return
	}
	c.Data(res.StatusCode, "application/json", raw)
}
