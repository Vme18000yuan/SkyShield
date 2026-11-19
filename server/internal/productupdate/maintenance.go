// Package productupdate coordinates the host script's service handoff.
package productupdate

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

// Maintenance blocks new application writes while the host script is stopping
// the services. The script checks active work again after creating the marker.
func Maintenance(marker string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			if _, err := os.Stat(marker); err == nil {
				httpdto.ErrorWithStatus(c, http.StatusServiceUnavailable, "UNAVAILABLE", "SYSTEM_UPDATING", "System update is in progress. Try again after the service restarts.")
				c.Abort()
				return
			}
		}
		c.Next()
	}
}
