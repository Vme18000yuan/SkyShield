package stream

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestFilePublishesBeforeProcessFinishesAndRetainsCompleteTailOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool.jsonl")
	observed := make(chan string, 3)
	release := make(chan struct{})
	failure := errors.New("tool failed later")
	done := make(chan error, 1)
	go func() {
		done <- File(context.Background(), path, 100, func(context.Context) error {
			file, err := os.Create(path)
			if err != nil {
				return err
			}
			defer file.Close()
			_, _ = file.WriteString("first\npar")
			<-release
			_, _ = file.WriteString("tial\ntail")
			return failure
		}, func(line string, _ bool) error { observed <- line; return nil })
	}()
	select {
	case line := <-observed:
		if line != "first" {
			t.Fatal(line)
		}
	case <-time.After(time.Second):
		t.Fatal("record was not delivered during execution")
	}
	select {
	case line := <-observed:
		t.Fatalf("partial line emitted: %q", line)
	default:
	}
	close(release)
	if err := <-done; !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if got := []string{<-observed, <-observed}; !reflect.DeepEqual(got, []string{"partial", "tail"}) {
		t.Fatal(got)
	}
}

func TestFileKeepsFramingAfterOversizedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rows")
	var lines []string
	var rejected int
	err := File(context.Background(), path, 3, func(context.Context) error { return os.WriteFile(path, []byte("abcdef\nok\n"), 0o600) }, func(line string, over bool) error {
		if over {
			rejected++
		} else {
			lines = append(lines, line)
		}
		return nil
	})
	if err != nil || rejected != 1 || !reflect.DeepEqual(lines, []string{"ok"}) {
		t.Fatalf("%v %d %v", err, rejected, lines)
	}
}

func TestSubmitFlushesBeforeReturningProducerFailure(t *testing.T) {
	failure := errors.New("later failure")
	var saved []int
	err := Submit(context.Background(), func(_ context.Context, items <-chan int) error {
		for item := range items {
			saved = append(saved, item)
		}
		return nil
	}, func(_ context.Context, emit func(int) error) error {
		if err := emit(7); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) || !reflect.DeepEqual(saved, []int{7}) {
		t.Fatalf("%v %v", err, saved)
	}
}

func TestSubmissionFailureCancelsProducer(t *testing.T) {
	failure := errors.New("database unavailable")
	err := Submit(context.Background(), func(context.Context, <-chan int) error { return failure }, func(ctx context.Context, _ func(int) error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
			return errors.New("producer was not cancelled")
		}
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
