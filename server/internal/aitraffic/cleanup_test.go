package aitraffic

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Autumn-27/artex/traffic"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

func TestWaitingReaderDoesNotRecreateDeletedTrafficScope(t *testing.T) {
	root := t.TempDir()
	manager, err := NewManager(root, func(*http.Request) (*url.URL, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	scope := Scope{TenantID: uuid.NewString(), UserID: 1, ConversationID: uuid.NewString()}
	dir := filepath.Join(root, scope.TenantID, "user-1", "traffic", "conversation-"+scope.ConversationID)
	if err = os.MkdirAll(filepath.Join(dir, "_index"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "_index", "index.sqlite"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".initialize.lock")
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		found, err := manager.Read(ctx, scope, func(*traffic.Traffic) error { t.Error("deleted traffic was opened"); return nil })
		if found {
			t.Error("deleted scope was found")
		}
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		fds, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, fd := range fds {
			value, _ := os.Readlink(filepath.Join("/proc/self/fd", fd.Name()))
			if value == path {
				count++
			}
		}
		if count >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reader did not wait for lock")
		}
		time.Sleep(time.Millisecond)
	}
	// Simulate another Server removing the scope while it holds this file lock.
	if err = os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("reader recreated deleted files", err)
	}
}
