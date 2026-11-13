package airuntime

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Autumn-27/norma/transcript"
	"os"
)

var ErrTranscriptMissing = errors.New("会话历史文件缺失或为空，无法安全续接；请恢复 AI 数据目录，或从新的会话/任务重新开始")

func requireTranscript(store *transcript.Store, session string) error {
	info, err := os.Stat(store.MainPath(session))
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return ErrTranscriptMissing
	}
	return nil
}

// A missing file must not silently turn resumed work into a fresh execution.
func guardTaskTranscript(ctx context.Context, db *sql.DB, store *transcript.Store, session, query string, args ...any) error {
	if requireTranscript(store, session) == nil {
		return nil
	}
	var required bool
	if err := db.QueryRowContext(ctx, query, args...).Scan(&required); err != nil {
		return err
	}
	if required {
		return ErrTranscriptMissing
	}
	return nil
}
