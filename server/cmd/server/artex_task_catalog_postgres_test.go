package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/auth"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	aihandler "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/handler"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	airouter "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/router"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyARTEXTaskCatalogPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var owner, other int
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='registry-admin'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := airepo.NewTaskStore(orm)
	facade := aiapp.NewTaskFacade(store, nil, nil)
	category, err := facade.SaveCategory(ctx, owner, aiapp.TaskCategory{Name: "  Web   测试  "}, true)
	if err != nil || category.Name != "Web 测试" {
		t.Fatalf("create task category: %+v %v", category, err)
	}
	if _, err = facade.SaveCategory(ctx, owner, aiapp.TaskCategory{Name: "web 测试"}, true); !errors.Is(err, aiapp.ErrTaskCatalogConflict) {
		t.Fatalf("category duplicate: %v", err)
	}
	category.Name = "Web 重命名"
	category, err = facade.SaveCategory(ctx, owner, category, false)
	if err != nil || category.Name != "Web 重命名" {
		t.Fatalf("rename task category: %+v %v", category, err)
	}
	foreign, err := facade.SaveCategory(ctx, other, aiapp.TaskCategory{Name: "私人分类"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = facade.SaveTemplate(ctx, owner, aiapp.TaskTemplate{Name: "foreign", Description: "描述", Goal: "目标", CategoryID: &foreign.ID}, true); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign template category: %v", err)
	}
	if _, err = facade.SaveTemplate(ctx, owner, aiapp.TaskTemplate{Name: "invalid", Description: "描述", Goal: "目标", InterceptRules: []aiapp.TaskTemplateRule{{Enabled: true, Action: "allow", Kind: "cidr", Pattern: "invalid"}}}, true); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("invalid template rule: %v", err)
	}
	template, err := facade.SaveTemplate(ctx, owner, aiapp.TaskTemplate{Name: "  外部 Web  ", Description: "测试对象", Goal: "验证接口", Remark: "仅测试窗口内", CategoryID: &category.ID, WorkerCount: 2, InterceptRules: []aiapp.TaskTemplateRule{{Enabled: true, Action: "allow", Kind: "fuzzy_domain", Pattern: " allowed.example "}}}, true)
	if err != nil || template.Name != "外部 Web" || len(template.InterceptRules) != 1 || template.InterceptRules[0].Pattern != "allowed.example" {
		t.Fatalf("create task template: %+v %v", template, err)
	}
	if _, err = facade.SaveTemplate(ctx, owner, aiapp.TaskTemplate{Name: "外部 web", Description: "描述", Goal: "目标"}, true); !errors.Is(err, aiapp.ErrTaskCatalogConflict) {
		t.Fatalf("template duplicate: %v", err)
	}
	template.Remark = "更新备注"
	template, err = facade.SaveTemplate(ctx, owner, template, false)
	if err != nil || template.Remark != "更新备注" {
		t.Fatalf("update task template: %+v %v", template, err)
	}
	otherTemplates, err := facade.Templates(ctx, other)
	if err != nil || len(otherTemplates) != 0 {
		t.Fatalf("template owner isolation: %+v %v", otherTemplates, err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO ai_task_template(tenant_id,owner_user_id,name,nkey,description,goal,category_id) SELECT tenant_id,$1,'forged','forged','描述','目标',id FROM ai_task_category WHERE id=$2`, other, category.ID); err == nil {
		t.Fatal("template accepted foreign category")
	}

	var taskIDs []int64
	for i := 0; i < 53; i++ {
		item, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, Name: fmt.Sprintf("Catalog Paging %02d", i), Goal: "分页测试", Remark: "记录", CategoryID: &category.ID})
		if err != nil {
			t.Fatal(err)
		}
		taskIDs = append(taskIDs, item.ID)
	}
	for _, id := range taskIDs[:2] {
		pinned := true
		if _, err = facade.UpdateMetadata(ctx, owner, id, aiapp.TaskMetadataUpdate{Pinned: &pinned}); err != nil {
			t.Fatal(err)
		}
	}
	name, remark := "Catalog Paging Updated", "新的备注"
	updated, err := facade.UpdateMetadata(ctx, owner, taskIDs[0], aiapp.TaskMetadataUpdate{Name: &name, Remark: &remark})
	if err != nil || updated.Name != name || updated.Remark != remark || updated.CategoryID == nil || *updated.CategoryID != category.ID || updated.CategoryName != category.Name || updated.PinnedAt == nil {
		t.Fatalf("task metadata: %+v %v", updated, err)
	}
	if _, err = facade.UpdateMetadata(ctx, other, taskIDs[0], aiapp.TaskMetadataUpdate{Name: &name}); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign task metadata: %v", err)
	}
	filter := aiapp.TaskListFilter{Query: "Catalog Paging", CategoryID: &category.ID, Status: "created"}
	first, err := facade.ListFiltered(ctx, owner, 0, filter)
	if err != nil || len(first) != 51 || first[0].ID != taskIDs[1] || first[1].ID != taskIDs[0] {
		t.Fatalf("pinned task ordering: first=%+v err=%v", first, err)
	}
	second, err := facade.ListFiltered(ctx, owner, first[49].ID, filter)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, item := range append(first[:50], second...) {
		if seen[item.ID] {
			t.Fatalf("duplicate task across pages: %d", item.ID)
		}
		seen[item.ID] = true
	}
	if len(seen) != 53 {
		t.Fatalf("task pagination lost results: %d", len(seen))
	}

	manager := auth.NewJWTManager("catalog-fixture", time.Minute, time.Hour)
	token, _, err := manager.GenerateAccessToken(owner, "registry-admin", 0)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/v1")
	group.Use(middleware.AuthMiddleware(manager, fixtureTokenVersions{}))
	airouter.RegisterAITaskRoutes(group, aihandler.NewTaskHandler(facade))
	request := func(method, path, body, authToken string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if authToken != "" {
			req.Header.Set("Authorization", "Bearer "+authToken)
		}
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, req)
		return out
	}
	if response := request(http.MethodGet, "/v1/aiTaskTemplates", "", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("templates without auth: %d", response.Code)
	}
	if response := request(http.MethodGet, "/v1/aiTaskTemplates", "", token); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"category_id":"`) {
		t.Fatalf("template HTTP string IDs: %d %s", response.Code, response.Body.String())
	}
	templateBody := fmt.Sprintf(`{"name":"HTTP 模板","description":"任务背景","goal":"任务目标","remark":"备注","worker_count":2,"category_id":"%d","intercept_rules":[{"enabled":true,"action":"allow","kind":"fuzzy_domain","pattern":"allowed.example"}]}`, category.ID)
	if response := request(http.MethodPost, "/v1/aiTaskTemplates", templateBody, token); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"pattern":"allowed.example"`) {
		t.Fatalf("template HTTP create: %d %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPost, "/v1/aiTaskTemplates", templateBody, token); response.Code != http.StatusConflict {
		t.Fatalf("template HTTP duplicate: %d %s", response.Code, response.Body.String())
	}
	var httpTemplate int64
	if err = db.QueryRowContext(ctx, `SELECT id FROM ai_task_template WHERE tenant_id=$1 AND owner_user_id=$2 AND name='HTTP 模板'`, updated.TenantID, owner).Scan(&httpTemplate); err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodDelete, fmt.Sprintf("/v1/aiTaskTemplates/%d", httpTemplate), "", token); response.Code != http.StatusOK {
		t.Fatalf("template HTTP delete: %d %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPost, "/v1/aiTaskCategories", `{"name":"HTTP 分类"}`, token); response.Code != http.StatusOK {
		t.Fatalf("category HTTP create: %d %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, fmt.Sprintf("/v1/aiTasks?category=%d&status=created&q=Catalog", category.ID), "", token); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Catalog Paging") {
		t.Fatalf("task filters HTTP: %d %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPatch, fmt.Sprintf("/v1/aiTasks/%d", taskIDs[0]), `{"categoryId":null,"pinned":false}`, token); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"categoryId":null`) {
		t.Fatalf("clear category HTTP: %d %s", response.Code, response.Body.String())
	}
	if err = facade.DeleteCategory(ctx, owner, category.ID); err != nil {
		t.Fatal(err)
	}
	updated, err = facade.Get(ctx, owner, taskIDs[1])
	if err != nil || updated.CategoryID != nil {
		t.Fatalf("deleting category did not retain task: %+v %v", updated, err)
	}
	templates, err := facade.Templates(ctx, owner)
	if err != nil || len(templates) != 1 || templates[0].CategoryID != nil || templates[0].ID != template.ID {
		t.Fatalf("deleting category did not retain template: %+v %v", templates, err)
	}
	if err = facade.DeleteTemplate(ctx, other, template.ID); !errors.Is(err, aiapp.ErrTaskNotFound) {
		t.Fatalf("foreign template deletion: %v", err)
	}
	if err = facade.DeleteTemplate(ctx, owner, template.ID); err != nil {
		t.Fatal(err)
	}
}
