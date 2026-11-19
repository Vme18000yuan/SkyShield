package oast

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

func (s *Service) RegisterRoutes(protected *gin.RouterGroup) {
	for _, base := range []string{"/aiConversations/:conversation/oastProbes", "/aiTasks/:task/oastProbes"} {
		protected.GET(base, s.handleList)
		protected.POST(base, s.handleCreate)
		protected.GET(base+"/:probe/events", s.handleEvents)
		// DELETE revokes the callback capability while retaining its evidence.
		protected.DELETE(base+"/:probe", s.handleRevoke)
	}
}

func requestScope(c *gin.Context) (int, Scope, bool) {
	user, ok := middleware.GetUserID(c)
	if !ok {
		c.AbortWithStatus(http.StatusUnauthorized)
		return 0, Scope{}, false
	}
	scope := Scope{ConversationID: c.Param("conversation")}
	if task := c.Param("task"); task != "" {
		id, err := strconv.ParseInt(task, 10, 64)
		if err != nil || id <= 0 {
			writeError(c, ErrInvalid)
			return 0, scope, false
		}
		scope.TaskID = id
	}
	c.Header("Cache-Control", "no-store")
	return user, scope, true
}
func writeError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrDisabled):
		status = http.StatusServiceUnavailable
	case errors.Is(err, ErrQuota):
		status = http.StatusTooManyRequests
	case errors.Is(err, ErrConflict):
		status = http.StatusConflict
	}
	c.JSON(status, gin.H{"error": publicError(err)})
}
func (s *Service) handleList(c *gin.Context) {
	user, scope, ok := requestScope(c)
	if !ok {
		return
	}
	out, err := s.List(c.Request.Context(), user, scope, c.Query("before"))
	if err != nil {
		writeError(c, err)
		return
	}
	httpdto.Success(c, out)
}
func (s *Service) handleCreate(c *gin.Context) {
	user, scope, ok := requestScope(c)
	if !ok {
		return
	}
	var in CreateInput
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		writeError(c, ErrInvalid)
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeError(c, ErrInvalid)
		return
	}
	if id, err := uuid.Parse(in.RequestID); err != nil || id.String() != in.RequestID {
		writeError(c, ErrInvalid)
		return
	}
	in.RequestID = "manual:" + in.RequestID
	out, err := s.Create(c.Request.Context(), user, scope, "", in)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, out)
}
func (s *Service) handleEvents(c *gin.Context) {
	user, scope, ok := requestScope(c)
	if !ok {
		return
	}
	wait := 0
	if value := c.Query("waitSeconds"); value != "" {
		var err error
		wait, err = strconv.Atoi(value)
		if err != nil {
			writeError(c, ErrInvalid)
			return
		}
	}
	out, err := s.Poll(c.Request.Context(), user, scope, c.Param("probe"), c.Query("after"), wait)
	if err != nil {
		writeError(c, err)
		return
	}
	httpdto.Success(c, out)
}
func (s *Service) handleRevoke(c *gin.Context) {
	user, scope, ok := requestScope(c)
	if !ok {
		return
	}
	if err := s.Revoke(c.Request.Context(), user, scope, c.Param("probe")); err != nil {
		writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
