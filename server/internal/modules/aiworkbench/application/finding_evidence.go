package application

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/evidence"
	"github.com/Autumn-27/artex/traffic"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"github.com/yyhuni/lunafox/server/internal/aitraffic"
)

type FindingEvidenceAccess interface {
	GetTask(context.Context, int, int64) (*Task, error)
	TrafficTaskTenant(context.Context, int, int64) (string, error)
	ReadFindingTraffic(context.Context, int, int64) (FindingTrafficRecord, error)
}
type FindingEvidenceFacade struct {
	store     FindingEvidenceAccess
	manager   TaskTrafficManager
	connector *airuntime.TaskGraphConnector
	root      string
}

func NewFindingEvidenceFacade(store FindingEvidenceAccess, manager TaskTrafficManager, connector *airuntime.TaskGraphConnector, root string) *FindingEvidenceFacade {
	return &FindingEvidenceFacade{store: store, manager: manager, connector: connector, root: root}
}
func (f *FindingEvidenceFacade) Bind(ctx context.Context, user int, taskID, findingID int64, refs []artexdb.TrafficRef) (*artexdb.FindingTraffic, error) {
	return f.bind(ctx, user, taskID, findingID, refs, "human_evidence", "")
}
func (f *FindingEvidenceFacade) BindReporter(ctx context.Context, user int, taskID, findingID int64, run string, refs []artexdb.TrafficRef) (*artexdb.FindingTraffic, error) {
	if !validConversationID(run) {
		return nil, ErrInvalidInput
	}
	return f.bind(ctx, user, taskID, findingID, refs, "reporter_evidence", run)
}
func (f *FindingEvidenceFacade) bind(ctx context.Context, user int, taskID, findingID int64, refs []artexdb.TrafficRef, mode, run string) (*artexdb.FindingTraffic, error) {
	if f == nil || f.store == nil || f.manager == nil || f.connector == nil || !filepath.IsAbs(f.root) || user <= 0 || taskID <= 0 || findingID <= 0 || len(refs) == 0 || len(refs) > 100 {
		return nil, ErrInvalidInput
	}
	for _, ref := range refs {
		if len(ref.TrafficID) > 128 || len(ref.Note) > 4000 || !utf8.ValidString(ref.TrafficID+ref.Note+ref.Role) || strings.ContainsRune(ref.TrafficID+ref.Note+ref.Role, '\x00') {
			return nil, ErrInvalidInput
		}
	}
	normalized, err := artexdb.NormalizeTrafficRefs(refs)
	if err != nil || len(normalized) == 0 {
		return nil, ErrInvalidInput
	}
	tenant, err := f.store.TrafficTaskTenant(ctx, user, taskID)
	if err != nil {
		return nil, err
	}
	task, err := f.store.GetTask(ctx, user, taskID)
	if err != nil {
		return nil, err
	}
	record, err := f.store.ReadFindingTraffic(ctx, user, findingID)
	if err != nil {
		return nil, err
	}
	if task.TenantID != tenant || record.TaskID != taskID {
		return nil, ErrTaskNotFound
	}
	connection, err := f.connector.OpenForExecution(ctx, airuntime.TaskExecution{ID: taskID, ExplorationID: task.ExplorationID, TenantID: tenant, UserID: user, RunID: run, ExecutionMode: mode})
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	var checked int64
	if err = connection.QueryRowContext(ctx, `SELECT id FROM tianqiong_ai.assert_finding_evidence_scope($1,$2)`, task.ExplorationID, findingID).Scan(&checked); err != nil || checked != taskID {
		return nil, ErrTaskNotFound
	}
	var result *artexdb.FindingTraffic
	found, err := f.manager.Read(ctx, aitraffic.Scope{TenantID: tenant, UserID: user, TaskID: taskID}, func(recorder *traffic.Traffic) error {
		dir := filepath.Join(f.root, tenant, fmt.Sprintf("user-%d", user), "evidence")
		store := evidence.New(&artexdb.DB{DB: connection}, recorder, dir)
		if mode == "reporter_evidence" {
			store.Guard = func(ctx context.Context, tx *sql.Tx) error {
				return checkEvidenceBindingEnabled(ctx, tx, tenant, task.ExplorationID, findingID)
			}
		}
		var e error
		result, e = store.Bind(ctx, findingID, normalized)
		return e
	})
	if errors.Is(err, sql.ErrNoRows) || err == nil && !found {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	for i := range result.Bindings {
		result.Bindings[i].Snapshot.ReqHead = ""
		result.Bindings[i].Snapshot.RespHead = ""
	}
	return result, nil
}

func (f *FindingEvidenceFacade) List(ctx context.Context, user int, taskID, findingID int64) (*artexdb.FindingTraffic, error) {
	if f == nil || f.store == nil || user <= 0 || taskID <= 0 || findingID <= 0 {
		return nil, ErrInvalidInput
	}
	tenant, err := f.store.TrafficTaskTenant(ctx, user, taskID)
	if err != nil {
		return nil, err
	}
	record, err := f.store.ReadFindingTraffic(ctx, user, findingID)
	if err != nil {
		return nil, err
	}
	if record.TaskID != taskID {
		return nil, ErrTaskNotFound
	}
	check, err := f.store.TrafficTaskTenant(ctx, user, taskID)
	if err != nil || check != tenant {
		if err == nil {
			err = ErrTaskNotFound
		}
		return nil, err
	}
	out := &artexdb.FindingTraffic{FindingID: record.FindingID, Version: record.Version, ReportVersion: record.ReportVersion, Bindings: record.Bindings}
	for i := range out.Bindings {
		out.Bindings[i].Snapshot.ReqHead = ""
		out.Bindings[i].Snapshot.RespHead = ""
	}
	return out, nil
}

type FindingEvidenceBody struct {
	Head   string `json:"head"`
	Data   []byte `json:"data"`
	Offset int64  `json:"offset"`
	Total  int64  `json:"total"`
}

func (f *FindingEvidenceFacade) Body(ctx context.Context, user int, taskID, findingID, bindingID int64, side string, offset, length int64) (FindingEvidenceBody, error) {
	var out FindingEvidenceBody
	if f == nil || f.store == nil || !filepath.IsAbs(f.root) || user <= 0 || taskID <= 0 || findingID <= 0 || bindingID <= 0 || offset < 0 || length < 1 || length > 8192 || (side != "request" && side != "response") {
		return out, ErrInvalidInput
	}
	tenant, err := f.store.TrafficTaskTenant(ctx, user, taskID)
	if err != nil {
		return out, err
	}
	record, err := f.store.ReadFindingTraffic(ctx, user, findingID)
	if err != nil {
		return out, err
	}
	if record.TaskID != taskID {
		return out, ErrTaskNotFound
	}
	var binding *artexdb.FindingTrafficBinding
	for i := range record.Bindings {
		if record.Bindings[i].ID == bindingID {
			binding = &record.Bindings[i]
			break
		}
	}
	if binding == nil {
		return out, ErrTaskNotFound
	}
	dir := filepath.Join(f.root, tenant, fmt.Sprintf("user-%d", user), "evidence")
	file, total, err := evidence.New(nil, nil, dir).OpenBody(binding.Snapshot, side)
	if err != nil {
		return out, err
	}
	defer file.Close()
	if offset > total {
		return out, ErrInvalidInput
	}
	data := make([]byte, min(length, total-offset))
	n, err := file.ReadAt(data, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return out, err
	}
	out = FindingEvidenceBody{Data: data[:n], Offset: offset, Total: total, Head: binding.Snapshot.ReqHead}
	if side == "response" {
		out.Head = binding.Snapshot.RespHead
	}
	check, err := f.store.TrafficTaskTenant(ctx, user, taskID)
	if err != nil || check != tenant {
		if err == nil {
			err = ErrTaskNotFound
		}
		return FindingEvidenceBody{}, err
	}
	return out, nil
}
func (f *FindingEvidenceFacade) Edit(ctx context.Context, user int, taskID, findingID, bindingID, version int64, role, note *string, remove bool, order []int64) (*artexdb.FindingTraffic, error) {
	if f == nil || f.store == nil || f.connector == nil || user <= 0 || taskID <= 0 || findingID <= 0 || bindingID < 0 || version < 0 || len(order) > 100 {
		return nil, ErrInvalidInput
	}
	if order == nil && bindingID <= 0 || order != nil && bindingID != 0 {
		return nil, ErrInvalidInput
	}
	if role != nil && !artexdb.ValidTrafficRole(*role) {
		return nil, ErrInvalidInput
	}
	if note != nil && (len(*note) > 4000 || !utf8.ValidString(*note) || strings.ContainsRune(*note, '\x00')) {
		return nil, ErrInvalidInput
	}
	seen := map[int64]bool{}
	for _, id := range order {
		if id <= 0 || seen[id] {
			return nil, ErrInvalidInput
		}
		seen[id] = true
	}
	tenant, err := f.store.TrafficTaskTenant(ctx, user, taskID)
	if err != nil {
		return nil, err
	}
	task, err := f.store.GetTask(ctx, user, taskID)
	if err != nil {
		return nil, err
	}
	record, err := f.store.ReadFindingTraffic(ctx, user, findingID)
	if err != nil {
		return nil, err
	}
	if record.TaskID != taskID || task.TenantID != tenant {
		return nil, ErrTaskNotFound
	}
	connection, err := f.connector.OpenForExecution(ctx, airuntime.TaskExecution{ID: taskID, ExplorationID: task.ExplorationID, TenantID: tenant, UserID: user, ExecutionMode: "human_evidence"})
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	var checked int64
	if err = connection.QueryRowContext(ctx, `SELECT id FROM tianqiong_ai.assert_finding_evidence_scope($1,$2)`, task.ExplorationID, findingID).Scan(&checked); err != nil || checked != taskID {
		return nil, ErrTaskNotFound
	}
	db := &artexdb.DB{DB: connection}
	if err = db.EditFindingTraffic(ctx, findingID, bindingID, version, role, note, remove, order); errors.Is(err, artexdb.ErrEvidenceConflict) {
		return nil, ErrEvidenceVersionConflict
	} else if errors.Is(err, artexdb.ErrEvidenceNotFound) {
		return nil, ErrTaskNotFound
	} else if err != nil {
		return nil, err
	}
	return f.List(ctx, user, taskID, findingID)
}

func checkEvidenceBindingEnabled(ctx context.Context, tx *sql.Tx, tenant string, exploration, findingID int64) error {
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(options->>'agent_traffic_binding','false')='true' FROM ai_runtime_setting WHERE tenant_id=$1::uuid FOR SHARE`, tenant).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !enabled {
		return ErrTaskControlConflict
	}
	if err != nil {
		return err
	}
	var checked int64
	return tx.QueryRowContext(ctx, `SELECT id FROM tianqiong_ai.assert_finding_evidence_scope($1,NULLIF($2,0))`, exploration, findingID).Scan(&checked)
}

// RecordFinding preserves source ARTEX's atomic finding + evidence transaction.
// The trusted task identity and lease come from the server's Agent context.
func (f *FindingEvidenceFacade) RecordFinding(ctx context.Context, in artexdb.RecordFindingInput, refs []artexdb.TrafficRef) (*artexdb.RecordedFinding, error) {
	task, ok := airuntime.TaskExecutionFromContext(ctx)
	if !ok || f == nil || f.store == nil || f.manager == nil || f.connector == nil || !filepath.IsAbs(f.root) || task.ID != in.TaskID || task.ExplorationID != in.ExplorationID || task.UserID <= 0 || len(refs) == 0 || len(refs) > 100 {
		return nil, ErrInvalidInput
	}
	for _, ref := range refs {
		if len(ref.TrafficID) > 128 || len(ref.Note) > 4000 || !utf8.ValidString(ref.TrafficID+ref.Note+ref.Role) || strings.ContainsRune(ref.TrafficID+ref.Note+ref.Role, '\x00') {
			return nil, ErrInvalidInput
		}
	}
	normalized, err := artexdb.NormalizeTrafficRefs(refs)
	if err != nil || len(normalized) == 0 {
		return nil, ErrInvalidInput
	}
	tenant, err := f.store.TrafficTaskTenant(ctx, task.UserID, task.ID)
	if err != nil || tenant != task.TenantID {
		if err == nil {
			err = ErrTaskNotFound
		}
		return nil, err
	}
	connection, err := f.connector.OpenForExecution(ctx, task)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	var result *artexdb.RecordedFinding
	found, err := f.manager.Read(ctx, aitraffic.Scope{TenantID: tenant, UserID: task.UserID, TaskID: task.ID}, func(recorder *traffic.Traffic) error {
		store := evidence.New(&artexdb.DB{DB: connection}, recorder, filepath.Join(f.root, tenant, fmt.Sprintf("user-%d", task.UserID), "evidence"))
		store.Guard = func(ctx context.Context, tx *sql.Tx) error {
			return checkEvidenceBindingEnabled(ctx, tx, tenant, task.ExplorationID, 0)
		}
		var e error
		result, e = store.Record(ctx, in, normalized)
		return e
	})
	if errors.Is(err, sql.ErrNoRows) || err == nil && !found {
		return nil, ErrTaskNotFound
	}
	return result, err
}

func (f *TaskFacade) SetFindingEvidence(facade *FindingEvidenceFacade) { f.evidence = facade }
func (f *TaskFacade) BindReporter(ctx context.Context, user int, taskID, findingID int64, run string, refs []artexdb.TrafficRef) (*artexdb.FindingTraffic, error) {
	if f.evidence == nil {
		return nil, ErrTaskNotFound
	}
	return f.evidence.BindReporter(ctx, user, taskID, findingID, run, refs)
}
