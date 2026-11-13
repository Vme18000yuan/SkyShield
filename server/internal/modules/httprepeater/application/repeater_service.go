package application

import (
	"context"
	"sync"
	"time"
)

type receiptKey struct {
	user int
	id   string
}
type Service struct {
	access    Access
	transport Transport
	mu        sync.Mutex
	active    int
	users     map[int]int
	receipts  map[receiptKey]time.Time
}

func NewService(access Access, transport Transport) *Service {
	return &Service{access: access, transport: transport, users: map[int]int{}, receipts: map[receiptKey]time.Time{}}
}
func (s *Service) Send(ctx context.Context, user int, in Input) (Result, error) {
	p, err := Prepare(in)
	if err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	guard, err := s.access.Permit(ctx, user, p.PolicyURLs())
	if err != nil {
		return Result{}, err
	}
	key := receiptKey{user, in.RequestID}
	now := time.Now()
	s.mu.Lock()
	for k, expires := range s.receipts {
		if now.After(expires) {
			delete(s.receipts, k)
		}
	}
	if _, ok := s.receipts[key]; ok {
		s.mu.Unlock()
		return Result{}, ErrDuplicate
	}
	if s.active >= 8 || s.users[user] >= 2 || len(s.receipts) >= 1024 {
		s.mu.Unlock()
		return Result{}, ErrBusy
	}
	s.receipts[key] = now.Add(10 * time.Minute)
	s.active++
	s.users[user]++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.active--
		s.users[user]--
		if s.users[user] == 0 {
			delete(s.users, user)
		}
		s.mu.Unlock()
	}()
	result := s.transport.Send(ctx, p, guard)
	result.RequestID = in.RequestID
	return result, nil
}
