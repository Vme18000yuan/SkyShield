package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aihandler "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/handler"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	airouter "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/router"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyAISchedulePostgresContract(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	owner, target, asset := taskFixtureScope(t, ctx, db, "schedule.example")
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	tasks := airepo.NewTaskStore(orm)
	task, err := aiapp.NewTaskFacade(tasks, nil, nil).Create(ctx, aiapp.CreateTaskInput{UserID: owner, Goal: "按计划核验", TargetID: target, AssetIDs: []int64{asset}})
	if err != nil {
		t.Fatal(err)
	}
	schedules := airepo.NewScheduleStore(orm)
	facade := aiapp.NewScheduleFacade(schedules, nil)
	create := func(name, start, end string, days []int) *aiapp.ScheduleRecord {
		record, err := facade.Save(ctx, 0, aiapp.ScheduleInput{UserID: owner, Calendar: artexdb.TaskScheduleInput{Name: name, Enabled: true, TimezoneMode: "beijing", ScheduleType: "weekly", Weekdays: days, StartTime: start, EndTime: end, TaskIDs: []int64{task.ID}}})
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	first := create("晚间", "18:00", "23:00", []int{1})
	second := create("跨夜", "22:00", "01:00", []int{1})
	// Display-only edits must not alter the window cursor or restart a missed window.
	var beforeMetadata time.Time
	if err := db.QueryRowContext(ctx, `SELECT updated_at FROM ai_schedule WHERE id=$1`, second.Item.ID).Scan(&beforeMetadata); err != nil {
		t.Fatal(err)
	}
	name, remark := "跨夜计划（已重命名）", "测试备注"
	if _, err := facade.UpdateMetadata(ctx, owner, second.Item.ID, &name, &remark); err != nil {
		t.Fatal(err)
	}
	var afterMetadata time.Time
	if err := db.QueryRowContext(ctx, `SELECT updated_at FROM ai_schedule WHERE id=$1`, second.Item.ID).Scan(&afterMetadata); err != nil || !afterMetadata.Equal(beforeMetadata) {
		t.Fatalf("metadata changed schedule window timestamp: %v", err)
	}
	updated, err := facade.Save(ctx, second.Item.ID, aiapp.ScheduleInput{UserID: owner, PreserveRemark: true, PreserveEnabled: true, Calendar: artexdb.TaskScheduleInput{Name: name, Enabled: false, TimezoneMode: "beijing", ScheduleType: "weekly", Weekdays: []int{1}, StartTime: "22:00", EndTime: "01:00", TaskIDs: []int64{task.ID}}})
	if err != nil || !updated.Item.Enabled || updated.Item.Remark != remark {
		t.Fatalf("partial schedule update lost settings: %+v %v", updated, err)
	}
	at := func(s string) time.Time {
		v, e := time.Parse(time.RFC3339, s)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	boot := at("2026-10-19T12:00:00+08:00")
	tick := func(when string) {
		t.Helper()
		if _, e := schedules.TickSchedules(ctx, at(when), boot); e != nil {
			t.Fatal(e)
		}
	}
	state := func(status string, queued bool, managed bool) {
		t.Helper()
		var actual string
		var q, m bool
		if e := db.QueryRowContext(ctx, `SELECT status,queued,paused_by_schedule FROM ai_task WHERE id=$1`, task.ID).Scan(&actual, &q, &m); e != nil || actual != status || q != queued || m != managed {
			t.Fatalf("task state=%s %v %v err=%v; want %s %v %v", actual, q, m, e, status, queued, managed)
		}
	}
	tick("2026-10-19T17:00:00+08:00")
	state("paused", false, true)
	// Competing replicas must admit a task once and record only one transition.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := schedules.TickSchedules(ctx, at("2026-10-19T18:00:00+08:00"), boot)
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	state("created", true, false)
	stats, _, err := facade.Get(ctx, owner, first.Item.ID)
	if err != nil || stats.RunCount != 1 || stats.LastRunAt == nil {
		t.Fatalf("schedule run statistics: %+v %v", stats, err)
	}
	var resumes int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM ai_schedule_history WHERE task_id=$1 AND action='resume'`, task.ID).Scan(&resumes); err != nil || resumes != 1 {
		t.Fatalf("duplicate schedule admission: %d %v", resumes, err)
	}
	tick("2026-10-19T23:30:00+08:00")
	state("created", true, false)
	tick("2026-10-20T01:00:00+08:00")
	state("paused", false, true)
	// A human pause must not be resumed by a later active window.
	if _, err := tasks.ControlTask(ctx, owner, task.ID, "pause", false); err != nil {
		t.Fatal(err)
	}
	tick("2026-10-26T19:00:00+08:00")
	state("paused", false, false)
	// Explicit run-now may resume a human pause. Its task override is durable.
	started, err := facade.RunNow(ctx, owner, first.Item.ID)
	if err != nil || started.Started != 1 || len(started.Errors) != 0 {
		t.Fatalf("run now: %+v %v", started, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE ai_task SET schedule_manual_until=$2 WHERE id=$1`, task.ID, at("2026-10-27T01:00:00+08:00")); err != nil {
		t.Fatal(err)
	}
	tick("2026-10-26T17:00:00+08:00")
	state("created", true, false)
	// Remove the schedule-wide manual flag once its actual window is entered.
	tick("2026-10-26T19:00:00+08:00")
	state("created", true, false)
	tick("2026-10-27T01:00:00+08:00")
	state("paused", false, true)
	// A stale 'running' schedule status cannot admit work after the window has
	// closed, even before the next reconciliation tick runs.
	if _, err := db.ExecContext(ctx, `UPDATE ai_task SET status='created',queued=TRUE,schedule_manual_until=NULL WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE ai_schedule SET status='running',schedule_type='once',run_date=CURRENT_DATE-2,end_date=CURRENT_DATE-2,start_time='00:00',end_time='00:01',weekdays='[]',last_window_key='once:old' WHERE id=$1`, first.Item.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := tasks.ClaimTask(ctx, uuid.NewString())
	if err != nil || (claimed != nil && claimed.ID == task.ID) {
		t.Fatalf("closed schedule admitted task: %+v %v", claimed, err)
	}
	state("paused", false, true)
	history, err := schedules.ScheduleHistory(ctx, owner, second.Item.ID)
	if err != nil || len(history) == 0 {
		t.Fatalf("missing schedule history: %+v %v", history, err)
	}
	var other int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if _, err := schedules.GetSchedule(ctx, other, first.Item.ID); err == nil {
		t.Fatal("schedule visible to another user")
	}
	if _, err := facade.Save(ctx, 0, aiapp.ScheduleInput{UserID: other, Calendar: artexdb.TaskScheduleInput{Name: "other", Enabled: true, ScheduleType: "weekly", Weekdays: []int{1}, StartTime: "18:00", TaskIDs: []int64{task.ID}}}); err == nil {
		t.Fatal("cross-owner task binding accepted")
	}
	verifyAIScheduleHTTPContract(t, facade, owner, other, task.ID, first.Item.ID)
	if _, err := db.ExecContext(ctx, `UPDATE ai_task SET status='done',queued=FALSE WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	result, err := facade.RunNow(ctx, owner, first.Item.ID)
	if err != nil || result.Started != 0 || len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "已结束") {
		t.Fatalf("run now did not report terminal task: %+v %v", result, err)
	}
	history, err = schedules.ScheduleHistory(ctx, owner, first.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	var recorded bool
	for _, item := range history {
		if !item.Success && item.TaskID != nil && *item.TaskID == task.ID && strings.Contains(item.Message, "已结束") {
			recorded = true
		}
	}
	if !recorded {
		t.Fatalf("terminal task failure not recorded: %+v", history)
	}
}

func verifyAIScheduleHTTPContract(t *testing.T, facade *aiapp.ScheduleFacade, owner, other int, taskID, scheduleID int64) {
	t.Helper()
	manager := auth.NewJWTManager("fixture-jwt-secret", time.Minute, time.Hour)
	ownerToken, _, err := manager.GenerateAccessToken(owner, "registry-admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, _, err := manager.GenerateAccessToken(other, "ai-other", 0)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(manager, fixtureTokenVersions{}))
	airouter.RegisterAIScheduleRoutes(group, aihandler.NewScheduleHandler(facade))
	request := func(method, path, token string, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	path := "/v1/aiSchedules/" + strconv.FormatInt(scheduleID, 10)
	if res := request(http.MethodGet, path, "", nil); res.Code != http.StatusUnauthorized {
		t.Fatalf("missing schedule token: %d", res.Code)
	}
	if res := request(http.MethodGet, path, otherToken, nil); res.Code != http.StatusNotFound {
		t.Fatalf("schedule leaked across users: %d", res.Code)
	}
	if res := request(http.MethodGet, path, ownerToken, nil); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"history"`) {
		t.Fatalf("schedule detail: %d %s", res.Code, res.Body.String())
	}
	input := map[string]any{"displayName": "HTTP 计划", "enabled": false, "timezoneMode": "beijing", "scheduleType": "once", "runDate": "2099-01-01", "startTime": "09:00", "taskIds": []string{strconv.FormatInt(taskID, 10)}}
	if res := request(http.MethodPost, "/v1/aiSchedules", ownerToken, input); res.Code != http.StatusCreated || !strings.Contains(res.Body.String(), "HTTP 计划") {
		t.Fatalf("schedule create: %d %s", res.Code, res.Body.String())
	}
	if res := request(http.MethodPost, path+"/actions", otherToken, map[string]any{"action": "disable"}); res.Code != http.StatusNotFound && res.Code != http.StatusForbidden {
		t.Fatalf("cross-owner control: %d %s", res.Code, res.Body.String())
	}
}
