package application

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Autumn-27/artex/sidequestion"
	"github.com/Autumn-27/norma/llm"
	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/airuntime"
	"strings"
	"sync"
	"time"
)

var ErrSnapshotUnavailable = errors.New("主会话尚无可用上下文，请先发送一条消息")

type SideHistory struct {
	Items      []sidequestion.Exchange `json:"items"`
	Current    *sidequestion.Exchange  `json:"current"`
	NextCursor int64                   `json:"next_cursor"`
	Snapshot   *SideSnapshotInfo       `json:"snapshot"`
}
type SideSnapshotInfo struct {
	CapturedAt time.Time          `json:"captured_at"`
	Model      sidequestion.Model `json:"model"`
	Available  bool               `json:"available"`
	Reason     string             `json:"reason"`
}
type SideAdmission struct {
	Exchange      sidequestion.Exchange
	Snapshot      sidequestion.Snapshot
	Memory        sidequestion.Memory
	SessionID     int64
	ModelQuestion string
	Created       bool
	ParentStatus  *SideConversationStatus
}

// The persisted run lease is authoritative; model context is only a snapshot.
type SideConversationStatus struct {
	RunID        string     `json:"run_id"`
	Status       string     `json:"status"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	ErrorMessage string     `json:"error,omitempty"`
	CheckedAt    time.Time  `json:"checked_at"`
}

func attachSideConversationStatus(admission *SideAdmission) {
	if admission.ParentStatus == nil {
		return
	}
	status := struct {
		*SideConversationStatus
		SnapshotAt             time.Time `json:"snapshot_at"`
		SnapshotFromEarlierRun bool      `json:"snapshot_from_earlier_run"`
	}{admission.ParentStatus, admission.Snapshot.CapturedAt, admission.Snapshot.CapturedAt.Before(admission.ParentStatus.StartedAt)}
	raw, err := json.Marshal(status)
	if err != nil {
		return
	}
	note := "【平台核实的主对话运行状态】以下状态读取自运行记录，而非模型推断；优先于历史上下文中的运行描述。failed、stopped、interrupted、completed、timeout、exhausted 均表示该轮当前没有继续运行。后台命令曾完成不代表主对话仍在运行。\n" + string(raw)
	admission.Snapshot.Request.System = append(append([]string{}, admission.Snapshot.Request.System...), note)
}

type SideQuestionStore interface {
	SaveSideSnapshot(context.Context, int, sidequestion.Parent, string, sidequestion.Snapshot) error
	ReadSideHistory(context.Context, int, sidequestion.Parent, int64) (SideHistory, error)
	AdmitSideQuestion(context.Context, int, sidequestion.Parent, string, string, string) (SideAdmission, error)
	UpdateSideExchange(context.Context, int, string, int64, string, sidequestion.Exchange) (bool, error)
	LoadSideReplay(context.Context, int, int64, int64, int64, int64) ([]sidequestion.Exchange, error)
	SaveSideMemory(context.Context, int, SideAdmission, string, sidequestion.Memory) error
	ClearSideQuestions(context.Context, int, sidequestion.Parent) error
	CancelSideQuestion(context.Context, int, string) error
	ReadSideExchange(context.Context, int, string) (sidequestion.Exchange, error)
}
type SideProviderResolver func(context.Context, int, sidequestion.Model) (llm.Provider, error)
type SideQuestionFacade struct {
	governor  *airuntime.ExecutionGovernor
	snapshots *sideSnapshotWriter
	store     SideQuestionStore
	provider  SideProviderResolver
	mentions  *ChatMentionFacade
	root      context.Context
	instance  string
	mu        sync.Mutex
	closing   bool
	workers   sync.WaitGroup
	cancel    sync.Map
}

func (f *SideQuestionFacade) SetExecutionGovernor(g *airuntime.ExecutionGovernor) { f.governor = g }

func NewSideQuestionFacade(store SideQuestionStore, provider SideProviderResolver) *SideQuestionFacade {
	return &SideQuestionFacade{store: store, provider: provider, instance: uuid.NewString()}
}
func (f *SideQuestionFacade) Start(ctx context.Context) {
	f.root = ctx
	f.snapshots = newSideSnapshotWriter(ctx, f.store)
}
func (f *SideQuestionFacade) Shutdown() {
	f.mu.Lock()
	f.closing = true
	f.mu.Unlock()
	f.cancel.Range(func(_, cancel any) bool { cancel.(context.CancelFunc)(); return true })
	f.workers.Wait()
	if f.snapshots != nil {
		<-f.snapshots.done
	}
}
func (f *SideQuestionFacade) Publish(ctx context.Context, user int, parent sidequestion.Parent, run string, snapshot sidequestion.Snapshot) {
	if ctx.Err() == nil {
		if f.snapshots != nil {
			f.snapshots.put(sideSnapshotWrite{user, parent, run, snapshot})
			return
		}
		_ = f.store.SaveSideSnapshot(ctx, user, parent, run, snapshot)
	}
}
func (f *SideQuestionFacade) History(ctx context.Context, user int, parent sidequestion.Parent, before int64) (SideHistory, error) {
	if user <= 0 || before < 0 {
		return SideHistory{}, ErrInvalidInput
	}
	return f.store.ReadSideHistory(ctx, user, parent, before)
}
func (f *SideQuestionFacade) Get(ctx context.Context, user int, id string) (sidequestion.Exchange, error) {
	if user <= 0 || !validConversationID(id) {
		return sidequestion.Exchange{}, ErrInvalidInput
	}
	return f.store.ReadSideExchange(ctx, user, id)
}
func (f *SideQuestionFacade) Ask(ctx context.Context, user int, parent sidequestion.Parent, question, clientID string) (sidequestion.Exchange, error) {
	question = strings.TrimSpace(question)
	if user <= 0 || question == "" || len(question) > 64*1024 || clientID == "" || len(clientID) > 128 {
		return sidequestion.Exchange{}, ErrInvalidInput
	}
	if f.root == nil || f.root.Err() != nil || f.provider == nil {
		return sidequestion.Exchange{}, ErrModelNotConfigured
	}
	agentQuestion := question
	if f.mentions != nil {
		var err error
		agentQuestion, err = f.mentions.Compose(ctx, user, question)
		if err != nil {
			return sidequestion.Exchange{}, err
		}
	}
	permitCtx, release, permitErr := f.governor.Acquire(f.root, user, "side", false)
	if permitErr != nil {
		return sidequestion.Exchange{}, permitErr
	}
	transferred := false
	defer func() {
		if !transferred {
			release()
		}
	}()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closing {
		return sidequestion.Exchange{}, ErrModelNotConfigured
	}
	admission, err := f.store.AdmitSideQuestion(ctx, user, parent, question, clientID, f.instance)
	if err != nil || !admission.Created {
		return admission.Exchange, err
	}
	admission.ModelQuestion = agentQuestion
	attachSideConversationStatus(&admission)
	runCtx, cancel := context.WithTimeout(permitCtx, 5*time.Minute)
	f.cancel.Store(admission.Exchange.ID, cancel)
	f.workers.Add(1)
	transferred = true
	go func() { defer release(); f.execute(runCtx, cancel, user, admission) }()
	return admission.Exchange, nil
}
func (f *SideQuestionFacade) execute(ctx context.Context, cancel context.CancelFunc, user int, admission SideAdmission) {
	defer f.workers.Done()
	defer f.cancel.Delete(admission.Exchange.ID)
	defer cancel()
	parent := admission.Snapshot.Parent
	ctx = airuntime.WithLLMUsageScope(ctx, airuntime.LLMUsageScope{UserID: user, TaskID: parent.TaskID, ExplorationID: parent.ExplorationID, ConversationID: parent.ConversationKey, Worker: "sidequestion"})
	ctx = airuntime.WithLLMUsageRole(ctx, "sidequestion")
	item := admission.Exchange
	var stateMu sync.Mutex
	var latest = item
	var storeMu sync.Mutex
	lastWrite := time.Time{}
	write := func(force bool) bool {
		storeMu.Lock()
		defer storeMu.Unlock()
		if !force && time.Since(lastWrite) < 200*time.Millisecond {
			return true
		}
		stateMu.Lock()
		value := latest
		stateMu.Unlock()
		writeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		valid, err := f.store.UpdateSideExchange(writeCtx, user, f.instance, admission.SessionID, item.ID, value)
		if err != nil || !valid {
			cancel()
			return false
		}
		lastWrite = time.Now()
		return true
	}
	pulseDone := make(chan struct{})
	go func() {
		defer close(pulseDone)
		timer := time.NewTicker(10 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if !write(true) {
					return
				}
			}
		}
	}()
	provider, err := f.provider(ctx, user, admission.Snapshot.Model)
	var answer sidequestion.Answer
	var info sidequestion.ContextInfo
	if err == nil {
		service := sidequestion.SideQuestionService{Provider: provider}
		question := item.Question
		if admission.ModelQuestion != "" {
			question = admission.ModelQuestion
		}
		answer, info, err = service.Respond(ctx, admission.Snapshot, question, sidequestion.Replay{Memory: admission.Memory, Load: func(ctx context.Context, after int64) ([]sidequestion.Exchange, error) {
			return f.store.LoadSideReplay(ctx, user, admission.SessionID, item.Generation, after, item.Ordinal)
		}, Save: func(ctx context.Context, memory sidequestion.Memory) error {
			return f.store.SaveSideMemory(ctx, user, admission, f.instance, memory)
		}}, sidequestion.ContextOptions{}, func(answer sidequestion.Answer, info sidequestion.ContextInfo) {
			stateMu.Lock()
			latest.Answer = answer.Text
			latest.Usage = answer.Usage
			latest.Context = info
			latest.Sequence++
			stateMu.Unlock()
			write(false)
		})
	}
	cancel()
	<-pulseDone
	stateMu.Lock()
	latest.Answer = answer.Text
	latest.Usage = answer.Usage
	latest.Context = info
	latest.Status = "completed"
	if err != nil {
		latest.Status = "failed"
		latest.Error = err.Error()
	}
	if errors.Is(err, context.Canceled) {
		latest.Status = "cancelled"
	}
	latest.Sequence++
	stateMu.Unlock()
	write(true)
}
func (f *SideQuestionFacade) Cancel(ctx context.Context, user int, id string) error {
	if user <= 0 || !validConversationID(id) {
		return ErrInvalidInput
	}
	if err := f.store.CancelSideQuestion(ctx, user, id); err != nil {
		return err
	}
	if cancel, ok := f.cancel.Load(id); ok {
		cancel.(context.CancelFunc)()
	}
	return nil
}
func (f *SideQuestionFacade) Clear(ctx context.Context, user int, parent sidequestion.Parent) error {
	if user <= 0 {
		return ErrInvalidInput
	}
	return f.store.ClearSideQuestions(ctx, user, parent)
}

func (f *ConversationFacade) SetSideQuestions(side *SideQuestionFacade) { f.sideQuestions = side }
