package oast

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type Store interface {
	Create(context.Context, int, Probe, CreateInput, string) (Probe, error)
	List(context.Context, int, Scope, int64) ([]Probe, error)
	Get(context.Context, int, Scope, string) (Probe, error)
	Events(context.Context, string, int64) ([]Event, error)
	Revoke(context.Context, int, Scope, string) error
	LookupToken(context.Context, string) (Probe, error)
	Append(context.Context, string, Event) error
	Cleanup(context.Context) error
	ResolveRun(context.Context, int, string, int64) (Scope, error)
}

type ConversationAccess interface {
	TrafficConversationTenant(context.Context, int, string) (string, error)
}
type TaskAccess interface {
	TrafficTaskTenant(context.Context, int, int64) (string, error)
}

type Service struct {
	store         Store
	config        Config
	conversations ConversationAccess
	tasks         TaskAccess
}

func New(store Store, cfg Config, conversations ConversationAccess, tasks TaskAccess) (*Service, error) {
	var err error
	cfg, err = cfg.Normalize()
	if err != nil {
		return nil, err
	}
	return &Service{store: store, config: cfg, conversations: conversations, tasks: tasks}, nil
}

func (scope Scope) key() string {
	if scope.TaskID > 0 {
		return "task/" + strconv.FormatInt(scope.TaskID, 10)
	}
	return "conversation/" + scope.ConversationID
}

func (s *Service) authorize(ctx context.Context, user int, scope Scope) error {
	if user <= 0 || scope.TaskID < 0 || (scope.TaskID > 0) == (scope.ConversationID != "") {
		return ErrInvalid
	}
	if scope.TaskID > 0 {
		if s.tasks == nil {
			return ErrNotFound
		}
		if _, err := s.tasks.TrafficTaskTenant(ctx, user, scope.TaskID); err != nil {
			return ErrNotFound
		}
	} else {
		id, err := uuid.Parse(scope.ConversationID)
		if err != nil || id.String() != scope.ConversationID {
			return ErrInvalid
		}
		if s.conversations == nil {
			return ErrNotFound
		}
		if _, err := s.conversations.TrafficConversationTenant(ctx, user, scope.ConversationID); err != nil {
			return ErrNotFound
		}
	}
	return nil
}

func normalizeInput(in CreateInput) (CreateInput, error) {
	in.Label = strings.TrimSpace(in.Label)
	if in.TTLSeconds == 0 {
		in.TTLSeconds = 3600
	}
	if in.TTLSeconds < 60 || in.TTLSeconds > 86400 || utf8.RuneCountInString(in.Label) > 200 || !utf8.ValidString(in.Label) || strings.ContainsRune(in.Label, '\x00') || len(in.RequestID) == 0 || len(in.RequestID) > 240 || !utf8.ValidString(in.RequestID) || strings.ContainsRune(in.RequestID, '\x00') {
		return in, ErrInvalid
	}
	return in, nil
}

func (s *Service) Create(ctx context.Context, user int, scope Scope, run string, in CreateInput) (Probe, error) {
	if err := s.authorize(ctx, user, scope); err != nil {
		return Probe{}, err
	}
	if s.config.PublicURL == "" {
		return Probe{}, ErrDisabled
	}
	in, err := normalizeInput(in)
	if err != nil {
		return Probe{}, err
	}
	var secret [20]byte
	if _, err = rand.Read(secret[:]); err != nil {
		return Probe{}, err
	}
	token := hex.EncodeToString(secret[:])
	p := Probe{ID: uuid.NewString(), Scope: scope, Token: token, UserID: user, Label: in.Label, RunID: run, HTTPURL: s.config.PublicURL + "/oast/" + token}
	if s.config.DNSListen != "" {
		p.DNSName = token + "." + s.config.DNSZone
	}
	p, err = s.store.Create(ctx, user, p, in, run)
	return project(p), err
}

func cursor(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0, ErrInvalid
	}
	return n, nil
}

func project(p Probe) Probe {
	p.State = "active"
	if p.RevokedAt != nil {
		p.State = "revoked"
	} else if !p.ExpiresAt.After(time.Now()) {
		p.State = "expired"
	} else if p.EventCount >= MaxEvents {
		p.State = "saturated"
	}
	return p
}

func (s *Service) List(ctx context.Context, user int, scope Scope, before string) (ProbePage, error) {
	out := ProbePage{Items: []Probe{}, Enabled: s.config.PublicURL != "", DNSEnabled: s.config.DNSListen != ""}
	if err := s.authorize(ctx, user, scope); err != nil {
		return out, err
	}
	n, err := cursor(before)
	if err != nil {
		return out, err
	}
	items, err := s.store.List(ctx, user, scope, n)
	if err != nil {
		return out, err
	}
	if len(items) > PageSize {
		items = items[:PageSize]
		out.NextBefore = strconv.FormatInt(items[len(items)-1].Sequence, 10)
	}
	for _, item := range items {
		out.Items = append(out.Items, project(item))
	}
	return out, nil
}

func (s *Service) get(ctx context.Context, user int, scope Scope, id string) (Probe, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Probe{}, ErrInvalid
	}
	if err := s.authorize(ctx, user, scope); err != nil {
		return Probe{}, err
	}
	p, err := s.store.Get(ctx, user, scope, id)
	return project(p), err
}

// Poll is incremental and cancellable. The durable per-probe cursor also works
// after process restarts; delayed callbacks remain visible until probe expiry.
func (s *Service) Poll(ctx context.Context, user int, scope Scope, id, after string, waitSeconds int) (EventPage, error) {
	out := EventPage{Items: []Event{}, NextAfter: after}
	n, err := cursor(after)
	if err != nil || waitSeconds < 0 || waitSeconds > 20 {
		return out, ErrInvalid
	}
	deadline := time.Now().Add(time.Duration(waitSeconds) * time.Second)
	for {
		p, err := s.get(ctx, user, scope, id)
		if err != nil {
			return out, err
		}
		out.Probe = p
		items, err := s.store.Events(ctx, id, n)
		if err != nil {
			return out, err
		}
		out.HasMore = len(items) > PageSize
		if out.HasMore {
			items = items[:PageSize]
		}
		out.Items = items
		if len(items) > 0 {
			out.NextAfter = strconv.FormatInt(items[len(items)-1].ID, 10)
			return out, nil
		}
		if p.State != "active" || !time.Now().Before(deadline) {
			return out, nil
		}
		timer := time.NewTimer(min(500*time.Millisecond, time.Until(deadline)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return out, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Service) Revoke(ctx context.Context, user int, scope Scope, id string) error {
	if _, err := s.get(ctx, user, scope, id); err != nil {
		return err
	}
	return s.store.Revoke(ctx, user, scope, id)
}

func (s *Service) Record(ctx context.Context, token string, event Event) error {
	if !tokenPattern.MatchString(token) || s.config.PublicURL == "" {
		return ErrNotFound
	}
	p, err := s.store.LookupToken(ctx, token)
	if err != nil {
		return err
	}
	if project(p).State != "active" {
		return ErrNotFound
	}
	if err = s.authorize(ctx, p.UserID, p.Scope); err != nil {
		return err
	}
	return s.store.Append(ctx, p.ID, event)
}

func (s *Service) runScope(ctx context.Context, user int, run string, task int64) (Scope, error) {
	if _, err := uuid.Parse(run); err != nil {
		return Scope{}, ErrNotFound
	}
	scope, err := s.store.ResolveRun(ctx, user, run, task)
	if err != nil {
		return scope, err
	}
	return scope, s.authorize(ctx, user, scope)
}

func publicError(err error) string {
	switch err {
	case ErrInvalid:
		return "反连参数无效：有效期为 60–86400 秒，等待时间为 0–20 秒。"
	case ErrDisabled:
		return "反连未配置，请设置 OAST_PUBLIC_URL（或 PUBLIC_URL）为被测目标可访问的地址。"
	case ErrQuota:
		return "反连探针数量已达上限，请停止闲置探针，或等待过期数据清理。"
	case ErrConflict:
		return "同一请求 ID 不能用于不同的反连参数。"
	case ErrNotFound:
		return "反连探针或所属对话/任务不存在、不可访问或运行已结束。"
	default:
		return "反连操作失败，请稍后重试。"
	}
}
