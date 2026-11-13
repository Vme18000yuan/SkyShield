package main

import (
	"context"
	"database/sql"
	"testing"

	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	airepo "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyARTEXCoverageGraphScalePostgres(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	owner, target, _ := taskFixtureScope(t, ctx, db, "coverage-scale.example")
	if _, err := db.ExecContext(ctx, `INSERT INTO subdomain(target_id,dns_name) SELECT $1,'node-'||n||'.coverage-scale.example' FROM generate_series(1,600) n`, target); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	facade := aiapp.NewTaskFacade(airepo.NewTaskStore(orm), nil, nil)
	task, err := facade.Create(ctx, aiapp.CreateTaskInput{UserID: owner, TargetID: target, Goal: "规模化覆盖图"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = facade.AddScope(ctx, owner, task.ID, aiapp.TaskScopeInput{Kind: "root_domain", Value: "coverage-scale.example"}); err != nil {
		t.Fatal(err)
	}
	graph, err := facade.CoverageGraph(ctx, owner, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if graph.Truncated || len(graph.Nodes) < 601 || len(graph.Edges) < 600 {
		t.Fatalf("coverage graph scale: nodes=%d edges=%d truncated=%t", len(graph.Nodes), len(graph.Edges), graph.Truncated)
	}
}
