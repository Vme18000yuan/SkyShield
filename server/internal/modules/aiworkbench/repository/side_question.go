package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/google/uuid"
	aiapp "github.com/yyhuni/lunafox/server/internal/modules/aiworkbench/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
	"time"
)

type sideSession struct {
	ID         int64
	Generation int64
	Snapshot   []byte
	Memory     []byte
}

func sideParent(ctx context.Context, tx *gorm.DB, user int, parent sidequestion.Parent) (sidequestion.Parent, error) {
	if parent.ConversationKey != "" {
		var exists bool
		if err := tx.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1`+ownedConversation+`)`, assets.DefaultTenantID, user, parent.ConversationKey).Scan(&exists).Error; err != nil {
			return parent, err
		}
		if !exists {
			return parent, aiapp.ErrConversationNotFound
		}
		return sidequestion.Parent{ConversationKey: parent.ConversationKey}, nil
	}
	if parent.TaskID <= 0 {
		return parent, aiapp.ErrInvalidInput
	}
	var exploration int64
	err := tx.WithContext(ctx).Raw(`SELECT task.exploration_id FROM ai_task task JOIN tenant_membership m ON m.tenant_id=task.tenant_id AND m.user_id=task.owner_user_id JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id LEFT JOIN target target ON target.id=task.target_id WHERE task.tenant_id=? AND task.owner_user_id=? AND task.id=? AND task.deleted_at IS NULL AND (task.original_target_id IS NULL OR target.id IS NOT NULL AND target.deleted_at IS NULL) AND t.status='active' AND u.is_active=TRUE`, assets.DefaultTenantID, user, parent.TaskID).Row().Scan(&exploration)
	if errors.Is(err, sql.ErrNoRows) {
		return parent, aiapp.ErrTaskNotFound
	}
	if err != nil {
		return parent, err
	}
	if parent.ExplorationID > 0 && parent.ExplorationID != exploration {
		return parent, aiapp.ErrTaskNotFound
	}
	parent.ExplorationID = exploration
	if parent.MainSeg < 0 || parent.IntentID > 0 && parent.MainSeg != 0 {
		return parent, aiapp.ErrInvalidInput
	}
	if parent.MainSeg > 0 {
		var exists bool
		if err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM tianqiong_ai.main_sessions WHERE exploration_id=? AND seq=?)`, exploration, parent.MainSeg).Scan(&exists).Error; err != nil {
			return parent, err
		}
		if !exists {
			return parent, aiapp.ErrTaskNotFound
		}
	}
	if parent.IntentID > 0 {
		var exists bool
		if err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM tianqiong_ai.exploration_nodes WHERE exploration_id=? AND id=? AND kind='intent' AND state<>'deleted')`, exploration, parent.IntentID).Scan(&exists).Error; err != nil {
			return parent, err
		}
		if !exists {
			return parent, aiapp.ErrTaskNotFound
		}
	}
	return parent, nil
}
func readSideSession(tx *gorm.DB, user int, parent sidequestion.Parent, lock bool) (sideSession, error) {
	var value sideSession
	query := `SELECT id,generation,snapshot,memory FROM ai_side_session WHERE tenant_id=? AND owner_user_id=? AND parent_key=?`
	if lock {
		query += ` FOR UPDATE`
	}
	err := tx.Raw(query, assets.DefaultTenantID, user, parent.Key()).Row().Scan(&value.ID, &value.Generation, &value.Snapshot, &value.Memory)
	return value, err
}
func expireSideExchanges(tx *gorm.DB, session int64) error {
	return tx.Exec(`UPDATE ai_side_exchange SET status='interrupted',state=state||'{"status":"interrupted","error":"服务中断或运行租约已过期"}'::jsonb WHERE session_id=? AND status='running' AND lease_until<=CURRENT_TIMESTAMP`, session).Error
}
func decodeSideExchange(raw []byte, generation, ordinal int64, status string) (sidequestion.Exchange, error) {
	var item sidequestion.Exchange
	err := unmarshalSideDocument(raw, &item)
	item.Generation = generation
	item.Ordinal = ordinal
	item.Status = status
	return item, err
}
func (s *ConversationStore) SaveSideSnapshot(ctx context.Context, user int, parent sidequestion.Parent, run string, snapshot sidequestion.Snapshot) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		parent, err := sideParent(ctx, tx, user, parent)
		if err != nil {
			return err
		}
		var active bool
		if parent.ConversationKey != "" {
			err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_run WHERE tenant_id=? AND owner_user_id=? AND conversation_id=? AND id=? AND status='running' AND lease_until>CURRENT_TIMESTAMP)`, assets.DefaultTenantID, user, parent.ConversationKey, run).Scan(&active).Error
		} else {
			err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_task WHERE tenant_id=? AND owner_user_id=? AND id=? AND tianqiong_ai.task_run_live(id,owner_user_id,?::uuid))`, assets.DefaultTenantID, user, parent.TaskID, run).Scan(&active).Error
		}
		if err != nil {
			return err
		}
		if !active {
			return aiapp.ErrRunLeaseLost
		}
		snapshot.Parent = parent
		blob, err := marshalSideDocument(snapshot)
		if err != nil {
			return err
		}
		var conversation any
		var task any
		if parent.ConversationKey != "" {
			conversation = parent.ConversationKey
		} else {
			task = parent.TaskID
		}
		return tx.Exec(`INSERT INTO ai_side_session(tenant_id,owner_user_id,parent_key,conversation_id,task_id,snapshot) VALUES(?,?,?,?,?,?::jsonb) ON CONFLICT(tenant_id,owner_user_id,parent_key) DO UPDATE SET snapshot=EXCLUDED.snapshot,updated_at=CURRENT_TIMESTAMP WHERE (COALESCE((ai_side_session.snapshot->>'run_id')::bigint,0),COALESCE((ai_side_session.snapshot->>'version')::bigint,0)) <= ((EXCLUDED.snapshot->>'run_id')::bigint,(EXCLUDED.snapshot->>'version')::bigint)`, assets.DefaultTenantID, user, parent.Key(), conversation, task, string(blob)).Error
	})
}
func (s *ConversationStore) ReadSideHistory(ctx context.Context, user int, parent sidequestion.Parent, before int64) (aiapp.SideHistory, error) {
	result := aiapp.SideHistory{Items: []sidequestion.Exchange{}}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		parent, err := sideParent(ctx, tx, user, parent)
		if err != nil {
			return err
		}
		session, err := readSideSession(tx, user, parent, true)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = expireSideExchanges(tx, session.ID); err != nil {
			return err
		}
		if len(session.Snapshot) > 0 {
			var snap sidequestion.Snapshot
			if err = unmarshalSideDocument(session.Snapshot, &snap); err != nil {
				return err
			}
			result.Snapshot = &aiapp.SideSnapshotInfo{CapturedAt: snap.CapturedAt, Model: snap.Model, Available: true}
		}
		rows, err := tx.Raw(`SELECT state,generation,ordinal,status FROM ai_side_exchange WHERE session_id=? AND generation=? AND (?=0 OR ordinal<?) ORDER BY ordinal DESC LIMIT 51`, session.ID, session.Generation, before, before).Rows()
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var generation, ordinal int64
			var status string
			if err = rows.Scan(&raw, &generation, &ordinal, &status); err != nil {
				return err
			}
			item, err := decodeSideExchange(raw, generation, ordinal, status)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, item)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if len(result.Items) > 50 {
			result.Items = result.Items[:50]
			result.NextCursor = result.Items[len(result.Items)-1].Ordinal
		}
		for l, r := 0, len(result.Items)-1; l < r; l, r = l+1, r-1 {
			result.Items[l], result.Items[r] = result.Items[r], result.Items[l]
		}
		for _, item := range result.Items {
			if item.Running() {
				value := item
				result.Current = &value
			}
		}
		return nil
	})
	return result, err
}
func (s *ConversationStore) AdmitSideQuestion(ctx context.Context, user int, parent sidequestion.Parent, question, client, instance string) (aiapp.SideAdmission, error) {
	var admission aiapp.SideAdmission
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		parent, err := sideParent(ctx, tx, user, parent)
		if err != nil {
			return err
		}
		session, err := readSideSession(tx, user, parent, true)
		if errors.Is(err, sql.ErrNoRows) {
			return aiapp.ErrSnapshotUnavailable
		}
		if err != nil {
			return err
		}
		if len(session.Snapshot) == 0 {
			return aiapp.ErrSnapshotUnavailable
		}
		if err = expireSideExchanges(tx, session.ID); err != nil {
			return err
		}
		var raw []byte
		var ordinal int64
		var status string
		err = tx.Raw(`SELECT state,ordinal,status FROM ai_side_exchange WHERE session_id=? AND generation=? AND client_request_id=?`, session.ID, session.Generation, client).Row().Scan(&raw, &ordinal, &status)
		if err == nil {
			admission.Exchange, err = decodeSideExchange(raw, session.Generation, ordinal, status)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var busy bool
		if err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_side_exchange WHERE session_id=? AND status='running')`, session.ID).Scan(&busy).Error; err != nil {
			return err
		}
		if busy {
			return aiapp.ErrConversationBusy
		}
		if err = unmarshalSideDocument(session.Snapshot, &admission.Snapshot); err != nil {
			return err
		}
		if err = unmarshalSideDocument(session.Memory, &admission.Memory); err != nil {
			return err
		}
		if parent.ConversationKey != "" {
			var status aiapp.SideConversationStatus
			err = tx.Raw(`SELECT id::text,CASE WHEN status IN ('running','stopping') AND lease_until<=CURRENT_TIMESTAMP THEN 'interrupted' ELSE status END,created_at,finished_at,error_message,CURRENT_TIMESTAMP FROM ai_run WHERE tenant_id=? AND owner_user_id=? AND conversation_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, assets.DefaultTenantID, user, parent.ConversationKey).Row().Scan(&status.RunID, &status.Status, &status.StartedAt, &status.FinishedAt, &status.ErrorMessage, &status.CheckedAt)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil {
				admission.ParentStatus = &status
			}
		}
		item := sidequestion.Exchange{ID: uuid.NewString(), ClientID: client, Generation: session.Generation, Question: question, Status: "running", Model: admission.Snapshot.Model, SnapshotAt: admission.Snapshot.CapturedAt, CreatedAt: time.Now().UTC(), Sequence: 1}
		blob, err := marshalSideDocument(item)
		if err != nil {
			return err
		}
		if err = tx.Raw(`INSERT INTO ai_side_exchange(id,session_id,generation,client_request_id,status,state,lease_owner,lease_until) VALUES(?,?,?,?,'running',?::jsonb,?,CURRENT_TIMESTAMP+INTERVAL '45 seconds') RETURNING ordinal`, item.ID, session.ID, session.Generation, client, string(blob), instance).Row().Scan(&item.Ordinal); err != nil {
			return err
		}
		admission.Exchange = item
		admission.SessionID = session.ID
		admission.Created = true
		return nil
	})
	return admission, err
}
func (s *ConversationStore) UpdateSideExchange(ctx context.Context, user int, instance string, sessionID int64, id string, item sidequestion.Exchange) (bool, error) {
	blob, err := marshalSideDocument(item)
	if err != nil {
		return false, err
	}
	result := s.db.WithContext(ctx).Exec(`UPDATE ai_side_exchange e SET status=?,state=?::jsonb,lease_until=CURRENT_TIMESTAMP+INTERVAL '45 seconds' FROM ai_side_session s WHERE e.id=? AND e.session_id=? AND e.lease_owner=? AND e.status='running' AND e.lease_until>CURRENT_TIMESTAMP AND s.id=e.session_id AND s.tenant_id=? AND s.owner_user_id=? AND s.generation=e.generation AND EXISTS(SELECT 1 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE m.tenant_id=s.tenant_id AND m.user_id=s.owner_user_id AND t.status='active' AND u.is_active=TRUE)`, item.Status, string(blob), id, sessionID, instance, assets.DefaultTenantID, user)
	return result.RowsAffected == 1, result.Error
}
func (s *ConversationStore) LoadSideReplay(ctx context.Context, user int, session, generation, after, before int64) ([]sidequestion.Exchange, error) {
	rows, err := s.db.WithContext(ctx).Raw(`SELECT e.state,e.ordinal,e.status FROM ai_side_exchange e JOIN ai_side_session s ON s.id=e.session_id WHERE s.tenant_id=? AND s.owner_user_id=? AND s.id=? AND s.generation=? AND e.generation=s.generation AND e.ordinal>? AND e.ordinal<? AND e.status='completed' ORDER BY e.ordinal LIMIT 100`, assets.DefaultTenantID, user, session, generation, after, before).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []sidequestion.Exchange{}
	for rows.Next() {
		var raw []byte
		var ordinal int64
		var status string
		if err = rows.Scan(&raw, &ordinal, &status); err != nil {
			return nil, err
		}
		item, err := decodeSideExchange(raw, generation, ordinal, status)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *ConversationStore) SaveSideMemory(ctx context.Context, user int, admission aiapp.SideAdmission, instance string, memory sidequestion.Memory) error {
	blob, err := marshalSideDocument(memory)
	if err != nil {
		return err
	}
	result := s.db.WithContext(ctx).Exec(`UPDATE ai_side_session s SET memory=?::jsonb WHERE s.id=? AND s.tenant_id=? AND s.owner_user_id=? AND s.generation=? AND EXISTS(SELECT 1 FROM ai_side_exchange e WHERE e.id=? AND e.session_id=s.id AND e.status='running' AND e.lease_owner=? AND e.lease_until>CURRENT_TIMESTAMP)`, string(blob), admission.SessionID, assets.DefaultTenantID, user, admission.Exchange.Generation, admission.Exchange.ID, instance)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return aiapp.ErrRunLeaseLost
	}
	return nil
}
func (s *ConversationStore) ClearSideQuestions(ctx context.Context, user int, parent sidequestion.Parent) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		parent, err := sideParent(ctx, tx, user, parent)
		if err != nil {
			return err
		}
		session, err := readSideSession(tx, user, parent, true)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = tx.Exec(`UPDATE ai_side_session SET generation=generation+1,memory='{}' WHERE id=?`, session.ID).Error; err != nil {
			return err
		}
		return tx.Exec(`DELETE FROM ai_side_exchange WHERE session_id=?`, session.ID).Error
	})
}
func (s *ConversationStore) ReadSideExchange(ctx context.Context, user int, id string) (sidequestion.Exchange, error) {
	var raw []byte
	var generation, ordinal int64
	var status string
	err := s.db.WithContext(ctx).Raw(`SELECT e.state,e.generation,e.ordinal,CASE WHEN e.status='running' AND e.lease_until<=CURRENT_TIMESTAMP THEN 'interrupted' ELSE e.status END FROM ai_side_exchange e JOIN ai_side_session s ON s.id=e.session_id JOIN tenant_membership m ON m.tenant_id=s.tenant_id AND m.user_id=s.owner_user_id JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id LEFT JOIN ai_task task ON task.id=s.task_id LEFT JOIN target target ON target.id=task.target_id WHERE e.id=? AND s.tenant_id=? AND s.owner_user_id=? AND s.generation=e.generation AND t.status='active' AND u.is_active=TRUE AND (s.task_id IS NULL OR task.id IS NOT NULL AND task.deleted_at IS NULL AND (task.original_target_id IS NULL OR target.id IS NOT NULL AND target.deleted_at IS NULL))`, id, assets.DefaultTenantID, user).Row().Scan(&raw, &generation, &ordinal, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return sidequestion.Exchange{}, aiapp.ErrConversationNotFound
	}
	if err != nil {
		return sidequestion.Exchange{}, err
	}
	return decodeSideExchange(raw, generation, ordinal, status)
}
func (s *ConversationStore) CancelSideQuestion(ctx context.Context, user int, id string) error {
	if _, err := s.ReadSideExchange(ctx, user, id); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Exec(`UPDATE ai_side_exchange e SET status='cancelled',state=state||'{"status":"cancelled"}'::jsonb FROM ai_side_session s WHERE e.id=? AND s.id=e.session_id AND s.tenant_id=? AND s.owner_user_id=? AND e.status='running'`, id, assets.DefaultTenantID, user).Error
}

var _ aiapp.SideQuestionStore = (*ConversationStore)(nil)
