package productupdate

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMaintenanceBlocksWritesDuringHostHandoff(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "maintenance")
	router := gin.New()
	router.Use(Maintenance(marker))
	router.Any("/fixture", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := func(method string, want int) {
		t.Helper()
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(method, "/fixture", nil))
		if recorder.Code != want {
			t.Fatalf("%s returned %d, want %d", method, recorder.Code, want)
		}
	}
	request(http.MethodPost, http.StatusNoContent)
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	request(http.MethodGet, http.StatusNoContent)
	request(http.MethodPost, http.StatusServiceUnavailable)
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, http.StatusNoContent)
}
