package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/gin-gonic/gin"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type scheduleApplication interface {
	List(context.Context, int) ([]aiapp.ScheduleRecord, error)
	Get(context.Context, int, int64) (*aiapp.ScheduleRecord, []aiapp.ScheduleHistory, error)
	Save(context.Context, int64, aiapp.ScheduleInput) (*aiapp.ScheduleRecord, error)
	Delete(context.Context, int, int64) error
	SetEnabled(context.Context, int, int64, bool) error
	RunNow(context.Context, int, int64) (aiapp.ScheduleRunResult, error)
}
type ScheduleHandler struct{ app scheduleApplication }

func NewScheduleHandler(app scheduleApplication) *ScheduleHandler { return &ScheduleHandler{app: app} }
func scheduleID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("schedule"), 10, 64)
	if err != nil || id <= 0 {
		httpdto.BadRequest(c, "Invalid schedule ID")
		return 0, false
	}
	return id, true
}
func scheduleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, aiapp.ErrScheduleNotFound):
		httpdto.NotFound(c, "AI schedule not found")
	case errors.Is(err, aiapp.ErrInvalidInput):
		httpdto.BadRequest(c, "Invalid AI schedule input")
	default:
		taskError(c, err)
	}
}
func scheduleDTO(record aiapp.ScheduleRecord) gin.H {
	item := record.Item
	ids := make([]string, 0, len(item.TaskIDs))
	for _, id := range item.TaskIDs {
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	now := time.Now()
	w := aiapp.EvaluateScheduleWindow(&item, now)
	var nextStart, nextEnd *time.Time
	if item.Enabled && item.Status != "completed" {
		nextStart = w.NextStart
		nextEnd = w.NextEnd
	}
	timezone := "Asia/Shanghai"
	if item.TimezoneMode == "system" {
		timezone = time.Local.String()
	}
	return gin.H{"id": strconv.FormatInt(item.ID, 10), "name": httpdto.AIScheduleName(item.ID), "displayName": item.Name, "remark": item.Remark, "enabled": item.Enabled,
		"timezoneMode": item.TimezoneMode, "scheduleType": item.ScheduleType, "runDate": item.RunDate, "endDate": item.EndDate, "weekdays": item.Weekdays,
		"startTime": item.StartTime, "endTime": item.EndTime, "startImmediately": item.StartImmediately, "status": item.Status, "lastError": item.LastError,
		"lastTransitionAt": item.LastTransitionAt, "createdAt": item.CreatedAt, "updatedAt": item.UpdatedAt, "taskIds": ids, "serverTime": now, "nextStart": nextStart, "nextEnd": nextEnd, "timezone": timezone, "runCount": record.RunCount, "lastRunAt": record.LastRunAt}
}
func (h *ScheduleHandler) List(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	items, err := h.app.List(c.Request.Context(), user)
	if err != nil {
		scheduleError(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, item := range items {
		out = append(out, scheduleDTO(item))
	}
	httpdto.Success(c, gin.H{"schedules": out})
}
func (h *ScheduleHandler) Get(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := scheduleID(c)
	if !ok {
		return
	}
	item, history, err := h.app.Get(c.Request.Context(), user, id)
	if err != nil {
		scheduleError(c, err)
		return
	}
	out := make([]gin.H, 0, len(history))
	for _, entry := range history {
		var task *string
		if entry.TaskID != nil {
			v := strconv.FormatInt(*entry.TaskID, 10)
			task = &v
		}
		out = append(out, gin.H{"id": strconv.FormatInt(entry.ID, 10), "taskId": task, "action": entry.Action, "success": entry.Success, "message": entry.Message, "createdAt": entry.CreatedAt})
	}
	result := scheduleDTO(*item)
	result["history"] = out
	httpdto.Success(c, result)
}

type scheduleRequest struct {
	DisplayName      string   `json:"displayName"`
	Remark           *string  `json:"remark"`
	Enabled          *bool    `json:"enabled"`
	TimezoneMode     string   `json:"timezoneMode"`
	ScheduleType     string   `json:"scheduleType"`
	RunDate          string   `json:"runDate"`
	EndDate          string   `json:"endDate"`
	Weekdays         []int    `json:"weekdays"`
	StartTime        string   `json:"startTime"`
	EndTime          string   `json:"endTime"`
	StartImmediately bool     `json:"startImmediately"`
	TaskIDs          []string `json:"taskIds"`
}

func (h *ScheduleHandler) save(c *gin.Context, id int64) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var req scheduleRequest
	if !httpdto.BindJSON(c, &req) {
		return
	}
	ids := make([]int64, 0, len(req.TaskIDs))
	for _, raw := range req.TaskIDs {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			httpdto.BadRequest(c, "Invalid task ID")
			return
		}
		ids = append(ids, value)
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	remark := ""
	if req.Remark != nil {
		remark = *req.Remark
	}
	item, err := h.app.Save(c.Request.Context(), id, aiapp.ScheduleInput{UserID: user, Remark: remark, PreserveRemark: id > 0 && req.Remark == nil, PreserveEnabled: id > 0 && req.Enabled == nil, Calendar: artexdb.TaskScheduleInput{Name: req.DisplayName, Enabled: enabled, TimezoneMode: req.TimezoneMode,
		ScheduleType: req.ScheduleType, RunDate: req.RunDate, EndDate: req.EndDate, Weekdays: req.Weekdays, StartTime: req.StartTime, EndTime: req.EndTime, StartImmediately: req.StartImmediately, TaskIDs: ids}})
	if err != nil {
		scheduleError(c, err)
		return
	}
	if id == 0 {
		httpdto.Created(c, scheduleDTO(*item))
	} else {
		httpdto.Success(c, scheduleDTO(*item))
	}
}
func (h *ScheduleHandler) Create(c *gin.Context) { h.save(c, 0) }
func (h *ScheduleHandler) Update(c *gin.Context) {
	id, ok := scheduleID(c)
	if ok {
		h.save(c, id)
	}
}

// UpdateMetadata changes the display name and/or remark without modifying the schedule window.
// PATCH /v1/aiSchedules/:schedule
func (h *ScheduleHandler) UpdateMetadata(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := scheduleID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024)
	var input struct {
		DisplayName *string `json:"displayName"`
		Remark      *string `json:"remark"`
	}
	if !httpdto.BindJSON(c, &input) {
		return
	}
	app, ok := h.app.(interface {
		UpdateMetadata(context.Context, int, int64, *string, *string) (*aiapp.ScheduleRecord, error)
	})
	if !ok {
		scheduleError(c, aiapp.ErrInvalidInput)
		return
	}
	item, err := app.UpdateMetadata(c.Request.Context(), user, id, input.DisplayName, input.Remark)
	if err != nil {
		scheduleError(c, err)
		return
	}
	httpdto.Success(c, scheduleDTO(*item))
}
func (h *ScheduleHandler) Delete(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := scheduleID(c)
	if !ok {
		return
	}
	if err := h.app.Delete(c.Request.Context(), user, id); err != nil {
		scheduleError(c, err)
		return
	}
	httpdto.Success(c, gin.H{"deleted": true})
}
func (h *ScheduleHandler) Action(c *gin.Context) {
	user, ok := conversationUser(c)
	if !ok {
		return
	}
	id, ok := scheduleID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var req struct {
		Action string `json:"action"`
	}
	if !httpdto.BindJSON(c, &req) {
		return
	}
	switch req.Action {
	case "enable", "disable":
		if err := h.app.SetEnabled(c.Request.Context(), user, id, req.Action == "enable"); err != nil {
			scheduleError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"enabled": req.Action == "enable"})
	case "run":
		result, err := h.app.RunNow(c.Request.Context(), user, id)
		if err != nil {
			scheduleError(c, err)
			return
		}
		httpdto.Success(c, gin.H{"started": result.Started, "errors": result.Errors})
	default:
		httpdto.BadRequest(c, "Invalid schedule action")
	}
}
