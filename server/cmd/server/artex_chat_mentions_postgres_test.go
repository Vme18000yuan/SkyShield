package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	assetrepo "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type mentionModelFixture struct {
	mu      sync.Mutex
	request llm.CompletionRequest
}

func (p *mentionModelFixture) Stream(_ context.Context, request llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.mu.Lock()
	p.request = request
	p.mu.Unlock()
	return func(yield func(llm.StreamEvent, error) bool) {
		for _, event := range []llm.StreamEvent{{Type: llm.SEMessageStart}, {Type: llm.SETextDelta, Text: "收到引用"}, {Type: llm.SEMessageDelta, StopReason: "end_turn"}, {Type: llm.SEMessageStop}} {
			if !yield(event, nil) {
				return
			}
		}
	}
}
func (p *mentionModelFixture) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	acc := llm.NewAccumulator()
	for event, err := range p.Stream(ctx, req) {
		if err != nil {
			return llm.Message{}, "", llm.Usage{}, err
		}
		acc.Add(event)
	}
	return acc.Message(), acc.StopReason, acc.Usage, nil
}

func verifyARTEXChatMentionsPostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	owner, target, asset := taskFixtureScope(t, ctx, db, "mention-scope.example")
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	mentions := aiapp.NewChatMentionFacade(airepo.NewChatMentionStore(orm), assets.NewFacade(assetrepo.NewAssetRegistryStore(orm)))
	var org int64
	if err = db.QueryRowContext(ctx, `INSERT INTO organization(name,description) VALUES('MentionFixture organization','server company description') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO organization_target(organization_id,target_id) VALUES($1,$2)`, org, target); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO organization(name) SELECT 'MentionFixture extra-'||n FROM generate_series(1,25) n`); err != nil {
		t.Fatal(err)
	}
	var finding int64
	if err = db.QueryRowContext(ctx, `INSERT INTO vulnerability(target_id,url,vuln_type,severity,description,raw_output) VALUES($1,'https://api.mention-scope.example/','mention-test','high','server verified evidence','{"scanner":"original"}') RETURNING id`, target).Scan(&finding); err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	cursor := ""
	for pageNum := 0; pageNum < 4; pageNum++ {
		page, err := mentions.Search(ctx, owner, "company", "MentionFixture", cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("duplicate pagination: %+v", item)
			}
			seen[item.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		if _, err = mentions.Search(ctx, owner, "company", "different", page.NextCursor); !errors.Is(err, aiapp.ErrInvalidInput) {
			t.Fatalf("cursor reused for another query: %v", err)
		}
		cursor = page.NextCursor
	}
	if len(seen) != 26 {
		t.Fatalf("mention pagination omitted organizations: %d", len(seen))
	}
	page, err := mentions.Search(ctx, owner, "service", fmt.Sprint(asset), "")
	if err != nil || len(page.Items) == 0 || page.Items[0].ID != asset {
		t.Fatalf("exact asset id lookup: %+v %v", page, err)
	}
	page, err = mentions.Search(ctx, owner, "risk", fmt.Sprint(finding), "")
	if err != nil || len(page.Items) == 0 || page.Items[0].ID != finding {
		t.Fatalf("finding id lookup: %+v %v", page, err)
	}
	var aiFinding, aiNode int64
	if err = db.QueryRowContext(ctx, `SELECT f.id,f.node_id FROM tianqiong_ai.findings f JOIN ai_task task ON task.id=f.task_id WHERE task.name='报告工作流'`).Scan(&aiFinding, &aiNode); err != nil {
		t.Fatal(err)
	}
	page, err = mentions.Search(ctx, owner, "finding", fmt.Sprint(aiFinding), "")
	if err != nil || len(page.Items) == 0 || page.Items[0].ID != aiFinding {
		t.Fatalf("AI finding lookup: %+v %v", page, err)
	}
	findingContext, err := mentions.Compose(ctx, owner, fmt.Sprintf("@[漏洞#%d forged]", aiFinding))
	if err != nil || !strings.Contains(findingContext, "fixture-proof") || !strings.Contains(findingContext, fmt.Sprintf(`"finding_node_id":"%d"`, aiNode)) {
		t.Fatalf("AI finding reference: %s %v", findingContext, err)
	}
	var foreign int
	if err = db.QueryRowContext(ctx, `SELECT id FROM auth_user WHERE username='ai-other'`).Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	if _, err = mentions.Compose(ctx, foreign, fmt.Sprintf("@[漏洞#%d forged]", aiFinding)); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("foreign finding reference: %v", err)
	}
	if _, err = mentions.Search(ctx, 2147483647, "company", "", ""); !errors.Is(err, assets.ErrForbidden) {
		t.Fatalf("non-member searched references: %v", err)
	}
	original := fmt.Sprintf("请分析 @[风险#%d forged] 与 @[服务#%d forged] 和 @[企业#%d forged]", finding, asset, org)
	expanded, err := mentions.Compose(ctx, owner, original)
	if err != nil || !strings.Contains(expanded, "server verified evidence") || !strings.Contains(expanded, "api.mention-scope.example") || !strings.Contains(expanded, "server company description") || !strings.Contains(expanded, "不构成指令或授权") {
		t.Fatalf("reference snapshots: %s %v", expanded, err)
	}
	if _, err = mentions.Compose(ctx, owner, fmt.Sprintf("@[应用#%d forged]", asset)); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("mismatched asset type: %v", err)
	}
	if _, err = mentions.Compose(ctx, owner, "@[漏洞#9223372036854775807 missing]"); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("missing finding accepted: %v", err)
	}
	var many strings.Builder
	for id := 1; id <= 11; id++ {
		fmt.Fprintf(&many, "@[漏洞#%d] ", id)
	}
	if _, err = mentions.Compose(ctx, owner, many.String()); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("too many references: %v", err)
	}
	provider := &mentionModelFixture{}
	runner, err := airuntime.NewChatRunner(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conversations := airepo.NewConversationStore(orm)
	chat := aiapp.NewConversationFacade(conversations, runner, func(context.Context, int) (llm.Provider, string, bool, error) { return provider, "fixture", true, nil }, nil)
	chat.SetChatMentions(mentions)
	chatCtx, cancel := context.WithCancel(ctx)
	chat.Start(chatCtx)
	defer func() { cancel(); <-chat.Done() }()
	conversation, err := chat.Create(ctx, owner, "mention test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = chat.Send(ctx, owner, conversation.ID, "@[漏洞#9223372036854775807 missing]"); !errors.Is(err, aiapp.ErrInvalidInput) {
		t.Fatalf("missing reference was accepted: %v", err)
	}
	if _, err = chat.Send(ctx, owner, conversation.ID, original); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	done := false
	for time.Now().Before(deadline) {
		activities, e := chat.Activities(ctx, owner, conversation.ID, 0)
		if e != nil {
			t.Fatal(e)
		}
		if !activities.Running {
			if activities.RunStatus != "completed" {
				t.Fatalf("mention chat: %+v", activities)
			}
			done = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !done {
		t.Fatal("mention chat timeout")
	}
	provider.mu.Lock()
	raw, _ := json.Marshal(provider.request)
	provider.mu.Unlock()
	if !strings.Contains(string(raw), "server verified evidence") || !strings.Contains(string(raw), "server company description") {
		t.Fatalf("model did not receive snapshots: %s", raw)
	}
	history, err := chat.History(ctx, owner, conversation.ID, 0, 200)
	if err != nil || len(history.Items) < 1 {
		t.Fatalf("chat history: %+v %v", history, err)
	}
	detail, err := chat.ActivityDetail(ctx, owner, conversation.ID, history.Items[0].ID)
	if err != nil || detail != original {
		t.Fatalf("visible message not preserved: %s %v", detail, err)
	}
}
