package airuntime

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type TaskGraphConnector struct{ config *pgx.ConnConfig }

func NewTaskGraphConnector(dsn string) (*TaskGraphConnector, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	return &TaskGraphConnector{config: config}, nil
}

func (c *TaskGraphConnector) open(ctx context.Context, task TaskExecution) (*sql.DB, error) {
	config := c.config.Copy()
	if config.RuntimeParams == nil {
		config.RuntimeParams = map[string]string{}
	}
	config.RuntimeParams["search_path"] = "tianqiong_ai,public"
	config.RuntimeParams["statement_timeout"] = "10000"
	config.RuntimeParams["lock_timeout"] = "5000"
	config.RuntimeParams["tianqiong.task_id"] = strconv.FormatInt(task.ID, 10)
	config.RuntimeParams["tianqiong.owner_user_id"] = strconv.Itoa(task.UserID)
	config.RuntimeParams["tianqiong.execution_id"] = task.RunID
	mode := "runtime"
	switch task.ExecutionMode {
	case "main_chat", "human_evidence", "reporter_evidence":
		mode = task.ExecutionMode
	}
	config.RuntimeParams["tianqiong.execution_mode"] = mode
	db := stdlib.OpenDB(*config)
	// Root execution admission caps the number of these execution-scoped pools.
	// Leave the main application pool capacity for heartbeats and stop requests.
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// OpenForExecution exposes the same fenced connection boundary to other
// server-owned task adapters and integration checks.
func (c *TaskGraphConnector) OpenForExecution(ctx context.Context, task TaskExecution) (*sql.DB, error) {
	return c.open(ctx, task)
}
