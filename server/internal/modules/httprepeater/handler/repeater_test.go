package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/auth"
	app "github.com/yyhuni/lunafox/server/internal/modules/httprepeater/application"
)

type appFixture struct {
	calls int
	user  int
	err   error
}

func (a *appFixture) Send(_ context.Context, user int, _ app.Input) (app.Result, error) {
	a.calls++
	a.user = user
	return app.Result{StatusCode: 401, ResponseRaw: "HTTP/1.1 401 Unauthorized\r\n\r\n", Sent: true}, a.err
}
func TestTarget401IsNotPlatformAuthenticationFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := &appFixture{}
	h := New(fixture)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userClaims", &auth.Claims{UserID: 7}) })
	r.POST("/httpRepeater:send", h.Send)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/httpRepeater:send", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 200 || fixture.calls != 1 || fixture.user != 7 || !strings.Contains(w.Body.String(), `"statusCode":401`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("target status confused with API: %d %s", w.Code, w.Body.String())
	}
	fixture.err = app.ErrForbidden
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/httpRepeater:send", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	r = gin.New()
	r.POST("/httpRepeater:send", h.Send)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/httpRepeater:send", strings.NewReader(`{}`)))
	if w.Code != 401 || fixture.calls != 2 {
		t.Fatal("unauthenticated call reached application")
	}
}
