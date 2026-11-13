package airuntime

import (
	"context"
	"errors"
	"github.com/Autumn-27/norma/llm"
	"sync"
	"testing"
)

type permitFixture struct {
	mu       sync.Mutex
	full     bool
	user     int
	kind     string
	released int
}

func (s *permitFixture) ClaimExecution(_ context.Context, user int, _, _, kind string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.user, s.kind = user, kind
	return !s.full, nil
}
func (s *permitFixture) RenewExecution(context.Context, string, string) (bool, error) {
	return true, nil
}
func (s *permitFixture) ReleaseExecution(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.released++
	return nil
}

func TestModelPermitUsesBoundOwnerAndConcreteProfile(t *testing.T) {
	store := &permitFixture{}
	g := NewExecutionGovernor(store)
	provider := BindLLMUsageUser(g.ProfileProvider(&recordingProvider{}, "profile-fixture"), 7)
	for _, err := range provider.Stream(t.Context(), llm.CompletionRequest{}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.user != 7 || store.kind != "model:profile-fixture" || store.released != 1 {
		t.Fatalf("wrong permit scope/release: %+v", store)
	}
}

func TestRootAdmissionRejectsCapacityWithoutStartingWork(t *testing.T) {
	g := NewExecutionGovernor(&permitFixture{full: true})
	if _, _, err := g.Acquire(t.Context(), 7, "chat", false); !errors.Is(err, ErrExecutionCapacity) {
		t.Fatalf("unexpected admission: %v", err)
	}
}
