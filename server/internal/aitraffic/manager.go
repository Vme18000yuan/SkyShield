// Package aitraffic hosts ARTEX's recorder inside account-owned workspaces.
package aitraffic

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/traffic"
	"github.com/Autumn-27/norma/tool"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

type Scope struct {
	TenantID       string
	UserID         int
	TaskID         int64
	ConversationID string
}

// ExistingScopes enumerates only on-disk recorders for the already-authorized
// owner. Database authorization is still required for every returned scope.
func (m *Manager) ExistingScopes(ctx context.Context, tenant string, user int) ([]Scope, error) {
	id, err := uuid.Parse(tenant)
	if ctx == nil || m == nil || err != nil || id.String() != tenant || user <= 0 || !filepath.IsAbs(m.root) || m.root == "/" {
		return nil, errors.New("invalid traffic owner")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return nil, errors.New("traffic manager is closed")
	}
	dir := filepath.Join(m.root, tenant, fmt.Sprintf("user-%d", user), "traffic")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Scope{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]Scope, 0, len(entries))
	for _, entry := range entries {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		scope := Scope{TenantID: tenant, UserID: user}
		name := entry.Name()
		if raw, ok := strings.CutPrefix(name, "task-"); ok {
			task, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil || task <= 0 || strconv.FormatInt(task, 10) != raw {
				continue
			}
			scope.TaskID = task
		} else if raw, ok := strings.CutPrefix(name, "conversation-"); ok {
			conversation, parseErr := uuid.Parse(raw)
			if parseErr != nil || conversation.String() != raw {
				continue
			}
			scope.ConversationID = raw
		} else {
			continue
		}
		if info, statErr := os.Stat(filepath.Join(dir, name, "_index", "index.sqlite")); statErr == nil && info.Mode().IsRegular() {
			result = append(result, scope)
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return nil, statErr
		}
	}
	return result, nil
}

func (s Scope) name() (string, error) {
	tenant, err := uuid.Parse(s.TenantID)
	if err != nil || tenant.String() != s.TenantID || s.UserID <= 0 {
		return "", errors.New("invalid traffic owner")
	}
	if s.TaskID > 0 && s.ConversationID == "" {
		return fmt.Sprintf("task-%d", s.TaskID), nil
	}
	conversation, err := uuid.Parse(s.ConversationID)
	if s.TaskID == 0 && err == nil && conversation.String() == s.ConversationID {
		return "conversation-" + s.ConversationID, nil
	}
	return "", errors.New("invalid traffic scope")
}

type upstreamPolicy func(*http.Request) (*url.URL, error)
type instance struct {
	scope    Scope
	recorder *traffic.Traffic
	listener net.Listener
	proxyURL string
	caBundle string
	done     chan struct{}
	refs     int
}
type Manager struct {
	root     string
	upstream upstreamPolicy
	mu       sync.Mutex
	entries  map[Scope]*instance
	building map[Scope]chan struct{}
	buildWG  sync.WaitGroup
	readWG   sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
	closed   bool
}
type Lease struct {
	manager *Manager
	entry   *instance
	once    sync.Once
}

func NewManager(root string, upstream func(*http.Request) (*url.URL, error)) (*Manager, error) {
	if upstream == nil {
		return nil, errors.New("invalid traffic storage or proxy policy")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{root: root, upstream: upstream, entries: make(map[Scope]*instance), building: make(map[Scope]chan struct{}), ctx: ctx, cancel: cancel}, nil
}
func (m *Manager) Acquire(ctx context.Context, scope Scope) (*Lease, error) {
	if ctx == nil {
		return nil, errors.New("missing traffic context")
	}
	if !filepath.IsAbs(m.root) || m.root == "/" {
		return nil, errors.New("invalid traffic storage root")
	}
	name, err := scope.name()
	if err != nil {
		return nil, err
	}
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, errors.New("traffic manager is closed")
		}
		if item := m.entries[scope]; item != nil {
			select {
			case <-item.done:
				m.mu.Unlock()
				return nil, errors.New("traffic recorder stopped unexpectedly")
			default:
			}
			item.refs++
			m.mu.Unlock()
			return &Lease{manager: m, entry: item}, nil
		}
		if pending := m.building[scope]; pending != nil {
			m.mu.Unlock()
			select {
			case <-pending:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-m.ctx.Done():
				return nil, errors.New("traffic manager is closed")
			}
		}
		pending := make(chan struct{})
		m.building[scope] = pending
		m.buildWG.Add(1)
		m.mu.Unlock()
		buildCtx, cancel := context.WithCancel(ctx)
		stopManager := context.AfterFunc(m.ctx, cancel)
		item, buildErr := m.build(buildCtx, scope, name)
		stopManager()
		cancel()
		m.mu.Lock()
		delete(m.building, scope)
		close(pending)
		if m.closed || ctx.Err() != nil {
			m.mu.Unlock()
			if item != nil {
				_ = item.recorder.Close()
				<-item.done
			}
			m.buildWG.Done()
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, errors.New("traffic manager is closed")
		}
		if buildErr == nil {
			m.entries[scope] = item
		}
		m.mu.Unlock()
		m.buildWG.Done()
		if buildErr != nil {
			return nil, buildErr
		}
		return &Lease{manager: m, entry: item}, nil
	}
}

func (m *Manager) build(ctx context.Context, scope Scope, name string) (*instance, error) {
	userRoot := filepath.Join(m.root, scope.TenantID, fmt.Sprintf("user-%d", scope.UserID))
	dir := filepath.Join(userRoot, "traffic", name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".initialize.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err = lockFile(ctx, lock); err != nil {
		return nil, err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	recorder, err := traffic.Open(dir, "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	fail := func(e error) (*instance, error) { _ = recorder.Close(); return nil, e }
	// The workspace itself is the execution sandbox's allowed filesystem root.
	// Its os.Root prevents a symlink in the workspace from redirecting the write.
	bundle, err := writeCABundle(filepath.Join(userRoot, "work"), name, recorder.CACertPath())
	if err != nil {
		return fail(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fail(err)
	}
	var token [32]byte
	if _, err = rand.Read(token[:]); err != nil {
		_ = listener.Close()
		return fail(err)
	}
	secret := hex.EncodeToString(token[:])
	item := &instance{scope: scope, recorder: recorder, listener: listener, proxyURL: fmt.Sprintf("http://%s:x@%s", secret, listener.Addr()), caBundle: bundle, done: make(chan struct{}), refs: 1}
	go func() {
		defer close(item.done)
		defer listener.Close()
		_ = recorder.ServeManaged(listener, secret, m.upstream)
	}()
	return item, nil
}
func lockFile(ctx context.Context, file *os.File) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func writeCABundle(workspace, name, certPath string) (string, error) {
	if err := os.MkdirAll(workspace, 0700); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err = root.MkdirAll(".traffic-ca", 0700); err != nil {
		return "", err
	}
	cert, err := os.ReadFile(certPath)
	if err != nil {
		return "", err
	}
	system, err := os.ReadFile("/etc/ssl/certs/ca-certificates.crt")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	token := uuid.NewString()
	tmp := filepath.Join(".traffic-ca", token+".tmp")
	dest := filepath.Join(".traffic-ca", name+".pem")
	file, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	defer root.Remove(tmp)
	if len(system) > 0 {
		_, err = file.Write(system)
		if err == nil {
			_, err = file.Write([]byte("\n"))
		}
	}
	if err == nil {
		_, err = file.Write(cert)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if err = root.Rename(tmp, dest); err != nil {
		return "", err
	}
	return filepath.Join(workspace, dest), nil
}

// Read opens only an existing scope. It does not start a proxy or create
// storage for an empty history, and holds the active instance while reading.
func (m *Manager) Read(ctx context.Context, scope Scope, visit func(*traffic.Traffic) error) (bool, error) {
	if ctx == nil || visit == nil {
		return false, errors.New("invalid traffic read")
	}
	name, err := scope.name()
	if err != nil {
		return false, err
	}
	if !filepath.IsAbs(m.root) || m.root == "/" {
		return false, errors.New("invalid traffic storage root")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false, errors.New("traffic manager is closed")
	}
	if item := m.entries[scope]; item != nil {
		select {
		case <-item.done:
			m.mu.Unlock()
			return false, errors.New("traffic recorder stopped unexpectedly")
		default:
		}
		item.refs++
		m.readWG.Add(1)
		m.mu.Unlock()
		defer m.readWG.Done()
		defer m.release(item)
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return true, visit(item.recorder)
	}
	m.readWG.Add(1)
	m.mu.Unlock()
	defer m.readWG.Done()
	dir := filepath.Join(m.root, scope.TenantID, fmt.Sprintf("user-%d", scope.UserID), "traffic", name)
	if _, err = os.Stat(filepath.Join(dir, "_index", "index.sqlite")); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".initialize.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return false, err
	}
	defer lock.Close()
	if err = lockFile(ctx, lock); err != nil {
		return false, err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	// A different process may have removed this scope while we waited on the
	// old lock inode. Read must not recreate a recorder after its parent deletion.
	currentLock, statErr := os.Stat(filepath.Join(dir, ".initialize.lock"))
	heldLock, heldErr := lock.Stat()
	if errors.Is(statErr, os.ErrNotExist) {
		return false, nil
	}
	if statErr != nil {
		return false, statErr
	}
	if heldErr != nil {
		return false, heldErr
	}
	if !os.SameFile(currentLock, heldLock) {
		return false, nil
	}
	if _, err = os.Stat(filepath.Join(dir, "_index", "index.sqlite")); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	store, err := traffic.Open(dir, "127.0.0.1:0")
	if err != nil {
		return false, err
	}
	defer store.Close()
	return true, visit(store)
}

// RemoveScope requires that no writer is using this recorder. The callback is
// confined by the caller to the deleted owner's files. Readers opened without
// a live recorder serialize through the same on-disk initialization lock.
func (m *Manager) ScopeInUse(scope Scope) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed || m.entries[scope] != nil || m.building[scope] != nil
}

func (m *Manager) RemoveScope(ctx context.Context, scope Scope, remove func() error) error {
	name, err := scope.name()
	if err != nil || remove == nil {
		return errors.New("invalid traffic cleanup")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.entries[scope] != nil || m.building[scope] != nil {
		return errors.New("traffic recorder is in use")
	}
	path := filepath.Join(m.root, scope.TenantID, fmt.Sprintf("user-%d", scope.UserID), "traffic", name, ".initialize.lock")
	lock, err := os.OpenFile(path, os.O_RDWR, 0600)
	if errors.Is(err, os.ErrNotExist) {
		return remove()
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	if err := ctx.Err(); err != nil {
		return err
	}
	return remove()
}

func (l *Lease) ProxyURL() string           { return l.entry.proxyURL }
func (l *Lease) CABundle() string           { return l.entry.caBundle }
func (l *Lease) Recorder() *traffic.Traffic { return l.entry.recorder }
func (l *Lease) Tools() []tool.CoreTool     { return l.entry.recorder.Tools() }
func (l *Lease) Close() {
	if l == nil {
		return
	}
	l.once.Do(func() { l.manager.release(l.entry) })
}
func (m *Manager) release(item *instance) {
	m.mu.Lock()
	if m.entries[item.scope] != item {
		m.mu.Unlock()
		return
	}
	item.refs--
	if item.refs > 0 {
		m.mu.Unlock()
		return
	}
	delete(m.entries, item.scope)
	m.mu.Unlock()
	_ = item.recorder.Close()
	<-item.done
}
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.cancel()
	items := make([]*instance, 0, len(m.entries))
	for key, item := range m.entries {
		delete(m.entries, key)
		items = append(items, item)
	}
	m.mu.Unlock()
	m.buildWG.Wait()
	m.readWG.Wait()
	for _, item := range items {
		_ = item.recorder.Close()
		<-item.done
	}
}
