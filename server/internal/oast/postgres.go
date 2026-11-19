package oast

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

type PostgresStore struct{ db *gorm.DB }

func NewPostgresStore(db *gorm.DB) *PostgresStore { return &PostgresStore{db: db} }

const probeColumns = `id::text,sequence,owner_user_id AS user_id,COALESCE(conversation_id::text,'') AS conversation_id,COALESCE(task_id,0) AS task_id,token,run_id,label,http_url,dns_name,created_at,expires_at,revoked_at,event_count,last_event_at`
const ownedProbe = `tenant_id=? AND owner_user_id=? AND scope_key=?`

func oneProbe(query *gorm.DB) (Probe, error) {
	var out Probe
	result := query.Scan(&out)
	if result.Error != nil {
		return out, result.Error
	}
	if result.RowsAffected != 1 {
		return out, ErrNotFound
	}
	return out, nil
}

func (s *PostgresStore) Create(ctx context.Context, user int, p Probe, in CreateInput, run string) (Probe, error) {
	var out Probe
	payload, _ := json.Marshal(struct {
		Label string
		TTL   int
	}{in.Label, in.TTLSeconds})
	hash := fmt.Sprintf("%x", sha256.Sum256(payload))
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serializes quota and replay decisions across all processes for this owner.
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?,0))`, fmt.Sprintf("oast/%s/%d", assets.DefaultTenantID, user)).Error; err != nil {
			return err
		}
		if run != "" {
			scope, err := resolveRun(tx, user, run, p.TaskID)
			if err != nil || scope != p.Scope {
				return ErrNotFound
			}
		}
		var existing struct{ ID, InputHash string }
		r := tx.Raw(`SELECT id::text,input_hash FROM oast_probe WHERE `+ownedProbe+` AND request_id=?`, assets.DefaultTenantID, user, p.Scope.key(), in.RequestID).Scan(&existing)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected > 0 {
			if existing.InputHash != hash {
				return ErrConflict
			}
			var err error
			out, err = oneProbe(tx.Raw(`SELECT `+probeColumns+` FROM oast_probe WHERE id=?::uuid`, existing.ID))
			return err
		}
		var count int64
		if err := tx.Raw(`SELECT count(*) FROM oast_probe WHERE tenant_id=? AND owner_user_id=? AND revoked_at IS NULL AND expires_at>clock_timestamp()`, assets.DefaultTenantID, user).Scan(&count).Error; err != nil {
			return err
		}
		if count >= 1000 {
			return ErrQuota
		}
		// Bound retained rows as well, including repeated create/revoke cycles.
		if err := tx.Raw(`SELECT count(*) FROM oast_probe WHERE tenant_id=? AND owner_user_id=?`, assets.DefaultTenantID, user).Scan(&count).Error; err != nil {
			return err
		}
		if count >= 5000 {
			return ErrQuota
		}
		var err error
		out, err = oneProbe(tx.Raw(`INSERT INTO oast_probe(id,tenant_id,owner_user_id,conversation_id,task_id,scope_key,run_id,request_id,input_hash,token,label,http_url,dns_name,expires_at)
          VALUES(?::uuid,?::uuid,?,NULLIF(?,'')::uuid,NULLIF(?,0),?,?,?,?,?,?,?,?,clock_timestamp()+make_interval(secs=>?)) RETURNING `+probeColumns,
			p.ID, assets.DefaultTenantID, user, p.ConversationID, p.TaskID, p.Scope.key(), run, in.RequestID, hash, p.Token, p.Label, p.HTTPURL, p.DNSName, in.TTLSeconds))
		return err
	})
	return out, err
}

func (s *PostgresStore) List(ctx context.Context, user int, scope Scope, before int64) ([]Probe, error) {
	out := []Probe{}
	err := s.db.WithContext(ctx).Raw(`SELECT `+probeColumns+` FROM oast_probe WHERE `+ownedProbe+` AND (?=0 OR sequence<?) ORDER BY sequence DESC LIMIT ?`, assets.DefaultTenantID, user, scope.key(), before, before, PageSize+1).Scan(&out).Error
	return out, err
}
func (s *PostgresStore) Get(ctx context.Context, user int, scope Scope, id string) (Probe, error) {
	return oneProbe(s.db.WithContext(ctx).Raw(`SELECT `+probeColumns+` FROM oast_probe WHERE `+ownedProbe+` AND id=?::uuid`, assets.DefaultTenantID, user, scope.key(), id))
}
func (s *PostgresStore) LookupToken(ctx context.Context, token string) (Probe, error) {
	return oneProbe(s.db.WithContext(ctx).Raw(`SELECT `+probeColumns+` FROM oast_probe WHERE token=? AND revoked_at IS NULL AND expires_at>clock_timestamp() AND event_count<?`, token, MaxEvents))
}
func (s *PostgresStore) Events(ctx context.Context, id string, after int64) ([]Event, error) {
	out := []Event{}
	err := s.db.WithContext(ctx).Raw(`SELECT id,received_at,protocol,remote_addr,method,host,path,headers,body_base64,query_name,query_type,truncated FROM oast_event WHERE probe_id=?::uuid AND id>? ORDER BY id LIMIT ?`, id, after, PageSize+1).Scan(&out).Error
	return out, err
}
func (s *PostgresStore) Revoke(ctx context.Context, user int, scope Scope, id string) error {
	r := s.db.WithContext(ctx).Exec(`UPDATE oast_probe SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE `+ownedProbe+` AND id=?::uuid`, assets.DefaultTenantID, user, scope.key(), id)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *PostgresStore) Append(ctx context.Context, id string, e Event) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// UPDATE locks the probe BEFORE allocating the event sequence. Incremental
		// readers cannot skip a late commit with a smaller event ID for this probe.
		r := tx.Exec(`UPDATE oast_probe SET event_count=event_count+1,last_event_at=clock_timestamp() WHERE id=?::uuid AND revoked_at IS NULL AND expires_at>clock_timestamp() AND event_count<?`, id, MaxEvents)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return ErrNotFound
		}
		return tx.Exec(`INSERT INTO oast_event(probe_id,protocol,remote_addr,method,host,path,headers,body_base64,query_name,query_type,truncated) VALUES(?::uuid,?,?,?,?,?,?,?,?,?,?)`, id, e.Protocol, e.RemoteAddr, e.Method, e.Host, e.Path, e.Headers, e.BodyBase64, e.QueryName, e.QueryType, e.Truncated).Error
	})
}

func (s *PostgresStore) Cleanup(ctx context.Context) error {
	return s.db.WithContext(ctx).Exec(`DELETE FROM oast_probe WHERE id IN (SELECT id FROM oast_probe WHERE expires_at<? ORDER BY expires_at LIMIT 1000)`, time.Now().UTC().Add(-Retention)).Error
}

func (s *PostgresStore) ResolveRun(ctx context.Context, user int, run string, task int64) (Scope, error) {
	return resolveRun(s.db.WithContext(ctx), user, run, task)
}
func resolveRun(tx *gorm.DB, user int, run string, task int64) (Scope, error) {
	if task > 0 {
		var live bool
		err := tx.Raw(`SELECT tianqiong_ai.task_run_live(?,?,?::uuid)`, task, user, run).Scan(&live).Error
		if err != nil {
			return Scope{}, err
		}
		if !live {
			return Scope{}, ErrNotFound
		}
		return Scope{TaskID: task}, nil
	}
	var id string
	err := tx.Raw(`SELECT r.conversation_id::text FROM ai_run r JOIN ai_conversation c ON c.id=r.conversation_id AND c.owner_user_id=r.owner_user_id AND c.tenant_id=r.tenant_id
      JOIN tenant_membership m ON m.tenant_id=r.tenant_id AND m.user_id=r.owner_user_id JOIN auth_user u ON u.id=m.user_id JOIN tenant t ON t.id=m.tenant_id
      WHERE r.id=?::uuid AND r.tenant_id=?::uuid AND r.owner_user_id=? AND r.status='running' AND r.lease_until>clock_timestamp() AND u.is_active AND t.status='active' AND m.role IN ('admin','analyst')`, run, assets.DefaultTenantID, user).Row().Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Scope{}, ErrNotFound
	}
	return Scope{ConversationID: id}, err
}

var _ Store = (*PostgresStore)(nil)
