package application

import (
	"context"
	"encoding/json"
	"iter"
	"sync/atomic"
	"testing"
	"time"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

type credentialRaceStore struct {
	enabled  atomic.Bool
	finished chan string
}

func (s *credentialRaceStore) Claim(context.Context) (*CredentialJob, error) { return nil, nil }
func (s *credentialRaceStore) Enabled(context.Context, *CredentialJob) (bool, error) {
	return s.enabled.Load(), nil
}
func (s *credentialRaceStore) Finish(_ context.Context, _ *CredentialJob, status string, _ []byte, _ json.RawMessage) error {
	s.finished <- status
	return nil
}
func (s *credentialRaceStore) Retry(context.Context, int, int64, int64) error { return nil }

type credentialRaceCodec struct{}

func (credentialRaceCodec) Seal(_ string, value string) ([]byte, error) { return []byte(value), nil }
func (credentialRaceCodec) Open(_ string, value []byte) (string, error) { return string(value), nil }
func (credentialRaceCodec) Fingerprint(string, []byte) (string, error)  { return "fixture", nil }

type credentialRaceModel struct{ started, cancelled, release chan struct{} }

func (m *credentialRaceModel) Stream(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(func(llm.StreamEvent, error) bool) {}
}
func (m *credentialRaceModel) Complete(ctx context.Context, _ llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	close(m.started)
	<-ctx.Done()
	close(m.cancelled)
	<-m.release
	return llm.Message{}, "", llm.Usage{}, ctx.Err()
}

func TestCredentialDisableThenReenableDoesNotPublishCancelledResult(t *testing.T) {
	store := &credentialRaceStore{finished: make(chan string, 1)}
	store.enabled.Store(true)
	model := &credentialRaceModel{started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	processor := NewCredentialProcessor(store, credentialRaceCodec{}, func(context.Context, int) (llm.Provider, string, string, bool, error) {
		return model, "fixture", "", true, nil
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		processor.process(context.Background(), &CredentialJob{IntelligenceID: 1, TaskID: 1, UserID: 1, Lease: "fixture", Fingerprint: "fixture", Content: "unstructured credential"})
	}()
	select {
	case <-model.started:
	case <-time.After(3 * time.Second):
		t.Fatal("model did not start")
	}
	store.enabled.Store(false)
	select {
	case <-model.cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("disable did not cancel model")
	}
	store.enabled.Store(true)
	close(model.release)
	select {
	case status := <-store.finished:
		if status != "queued" {
			t.Fatalf("cancelled result published after re-enable: %s", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("job did not finish")
	}
	<-done
}

type bindingChangedStore struct{ statuses []string }

func (s *bindingChangedStore) Claim(context.Context) (*CredentialJob, error) { return nil, nil }
func (s *bindingChangedStore) Enabled(context.Context, *CredentialJob) (bool, error) {
	return true, nil
}
func (s *bindingChangedStore) Retry(context.Context, int, int64, int64) error { return nil }
func (s *bindingChangedStore) Finish(_ context.Context, _ *CredentialJob, status string, _ []byte, _ json.RawMessage) error {
	s.statuses = append(s.statuses, status)
	if status == "ready" {
		return ErrCredentialBindingStateChanged
	}
	return nil
}

type bindingChangedMatcher struct{}

func (bindingChangedMatcher) Match(context.Context, *CredentialJob, string, string, []artexdb.CredentialAccount) ([]artexdb.CredentialTraffic, string, string, *int64, error) {
	epoch := int64(1)
	return []artexdb.CredentialTraffic{{ID: "fixture"}}, "ready", "", &epoch, nil
}
func TestCredentialBindingChangeRequeuesWithoutPublishing(t *testing.T) {
	store := &bindingChangedStore{}
	processor := NewCredentialProcessor(store, credentialRaceCodec{}, nil)
	processor.SetTrafficMatcher(bindingChangedMatcher{})
	processor.process(context.Background(), &CredentialJob{IntelligenceID: 1, TaskID: 1, UserID: 1, Lease: "fixture", Fingerprint: "fixture", Content: `{"username":"alice","password":"s3cret-pass"}`})
	if len(store.statuses) != 2 || store.statuses[0] != "ready" || store.statuses[1] != "queued" {
		t.Fatalf("binding change did not requeue: %v", store.statuses)
	}
}

func TestCredentialTrafficRecheckReusesPreviousExtraction(t *testing.T) {
	content := "原文账号 alice 密码 p@ssword"
	previous := artexdb.CredentialAnalysis{State: "ready", SourceHash: artexdb.CredentialSourceHash(content), Method: "agent", Accounts: []artexdb.CredentialAccount{{Username: "alice", Password: "p@ssword"}}}
	raw, _ := json.Marshal(previous)
	store := &credentialRaceStore{finished: make(chan string, 1)}
	store.enabled.Store(true)
	var calls atomic.Int32
	processor := NewCredentialProcessor(store, credentialRaceCodec{}, func(context.Context, int) (llm.Provider, string, string, bool, error) {
		calls.Add(1)
		return nil, "", "", false, nil
	})
	processor.SetTrafficMatcher(bindingChangedMatcher{})
	processor.process(context.Background(), &CredentialJob{IntelligenceID: 1, TaskID: 1, UserID: 1, Lease: "fixture", Fingerprint: "fixture", Content: content, PreviousSealed: raw})
	if status := <-store.finished; status != "ready" || calls.Load() != 0 {
		t.Fatalf("recheck repeated model extraction: status=%s calls=%d", status, calls.Load())
	}
}
