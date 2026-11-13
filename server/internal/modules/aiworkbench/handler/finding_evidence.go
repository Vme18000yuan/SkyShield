package handler

import (
	"net/http"
	"strconv"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

func (h *TaskHandler) SetFindingEvidence(facade *aiapp.FindingEvidenceFacade) { h.evidence = facade }
func findingTrafficIDs(c *gin.Context) (int, int64, int64, bool) {
	user, ok := conversationUser(c)
	if !ok {
		return 0, 0, 0, false
	}
	task, ok := taskID(c)
	if !ok {
		return 0, 0, 0, false
	}
	finding, err := strconv.ParseInt(c.Param("finding"), 10, 64)
	if err != nil || finding <= 0 {
		httpdto.BadRequest(c, "Invalid finding ID")
		return 0, 0, 0, false
	}
	return user, task, finding, true
}
func (h *TaskHandler) FindingTraffic(c *gin.Context) {
	user, task, finding, ok := findingTrafficIDs(c)
	if !ok {
		return
	}
	if h.evidence == nil {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	c.Header("Cache-Control", "no-store")
	if c.Request.Method == http.MethodGet {
		out, err := h.evidence.List(c.Request.Context(), user, task, finding)
		if err != nil {
			taskError(c, err)
			return
		}
		httpdto.Success(c, out)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var input struct {
		Refs []artexdb.TrafficRef `json:"traffic_refs"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	out, err := h.evidence.Bind(c.Request.Context(), user, task, finding, input.Refs)
	if err != nil {
		taskError(c, err)
		return
	}
	httpdto.Success(c, out)
}
func (h *TaskHandler) EditFindingTraffic(c *gin.Context) {
	user, task, finding, ok := findingTrafficIDs(c)
	if !ok {
		return
	}
	if h.evidence == nil {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	var binding int64
	var err error
	if c.Param("binding") != "" {
		binding, err = strconv.ParseInt(c.Param("binding"), 10, 64)
		if err != nil || binding <= 0 {
			httpdto.BadRequest(c, "Invalid binding ID")
			return
		}
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var input struct {
		Version *int64   `json:"version"`
		Role    *string  `json:"role"`
		Note    *string  `json:"note"`
		Order   []string `json:"binding_ids"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	if input.Version == nil {
		taskError(c, aiapp.ErrInvalidInput)
		return
	}
	var order []int64
	if c.Request.Method == http.MethodPut {
		if input.Order == nil {
			taskError(c, aiapp.ErrInvalidInput)
			return
		}
		order = make([]int64, 0, len(input.Order))
		for _, raw := range input.Order {
			id, e := strconv.ParseInt(raw, 10, 64)
			if e != nil || id <= 0 {
				taskError(c, aiapp.ErrInvalidInput)
				return
			}
			order = append(order, id)
		}
	}
	out, err := h.evidence.Edit(c.Request.Context(), user, task, finding, binding, *input.Version, input.Role, input.Note, c.Request.Method == http.MethodDelete, order)
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, out)
}
func (h *TaskHandler) FindingTrafficBody(c *gin.Context) {
	user, task, finding, ok := findingTrafficIDs(c)
	if !ok {
		return
	}
	if h.evidence == nil {
		taskError(c, aiapp.ErrTaskNotFound)
		return
	}
	binding, err := strconv.ParseInt(c.Param("binding"), 10, 64)
	if err != nil || binding <= 0 {
		taskError(c, aiapp.ErrInvalidInput)
		return
	}
	offset, ok := trafficInt(c, "offset", 0)
	if !ok {
		return
	}
	length, ok := trafficInt(c, "length", 8192)
	if !ok {
		return
	}
	side := c.DefaultQuery("side", "response")
	out, err := h.evidence.Body(c.Request.Context(), user, task, finding, binding, side, offset, length)
	if err != nil {
		taskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpdto.Success(c, out)
}
