// Copyright SkyShield contributors. SPDX-License-Identifier: AGPL-3.0-only
package traffic

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// DeleteExchanges removes exact exchange IDs and their indexed bodies. Shared
// blobs are reclaimed only after the last reference is gone. Evidence copies
// bound to findings live in a separate store and remain intact.
func (t *Traffic) DeleteExchanges(ids []string) (int64, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return 0, errors.New("select 1 to 100 traffic records")
	}
	args := make([]any, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || len(id) > 128 || strings.ContainsRune(id, 0) || seen[id] {
			return 0, errors.New("invalid traffic ID")
		}
		seen[id] = true
		args = append(args, id)
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	where := "id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
	rows, err := t.db.Query(`SELECT DISTINCT path FROM exchanges WHERE path<>'' AND `+where, args...)
	if err != nil {
		return 0, err
	}
	var legacy []string
	for rows.Next() {
		var path string
		if err = rows.Scan(&path); err != nil {
			rows.Close()
			return 0, err
		}
		legacy = append(legacy, path)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	tx, err := t.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n, err := t.deleteWhere(tx, where, args...)
	if err != nil {
		return 0, err
	}
	root, err := os.OpenRoot(t.dir)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	var trees []string
	for _, path := range legacy {
		if path == "." || filepath.IsAbs(path) || filepath.Clean(path) != path || strings.HasPrefix(path, "../") {
			return 0, errors.New("invalid legacy traffic path")
		}
		var retained int
		if err = tx.QueryRow(`SELECT count(*) FROM exchanges WHERE path=?`, path).Scan(&retained); err != nil {
			return 0, err
		}
		if retained > 0 {
			continue
		}
		info, err := root.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return 0, errors.New("invalid legacy traffic directory")
		}
		if _, err = root.Stat(path); err != nil {
			return 0, err
		}
		trees = append(trees, filepath.Join(t.dir, path))
	}
	stage, moves, err := t.stageTrees(trees)
	if err != nil {
		return 0, errors.Join(err, restoreTrees(stage, moves))
	}
	if err = recordDeleteJournal(stage, moves, ids); err != nil {
		return 0, errors.Join(err, restoreTrees(stage, moves))
	}
	if err = tx.Commit(); err != nil {
		return 0, errors.Join(err, restoreTrees(stage, moves))
	}
	t.reapStage(stage)
	if n > 0 {
		t.reclaim()
	}
	// A retry after committed rows but failed blob cleanup must still reclaim.
	if err = t.gcBlobs(); err != nil {
		return n, err
	}
	return n, nil
}

func recordDeleteJournal(stage string, moves []stagedTrafficPath, ids []string) error {
	return deletionJournal(stage, moves, ids, false)
}

func deletionJournal(stage string, moves []stagedTrafficPath, ids []string, clearAll bool) error {
	if stage == "" {
		return nil
	}
	journal := hostDeleteStageJournal{Version: 2, RecordIDs: ids, ClearAll: clearAll}
	for _, move := range moves {
		journal.Moves = append(journal.Moves, hostDeleteStageMove{Source: move.source, Staged: move.staged})
	}
	temporary := filepath.Join(stage, "records.tmp")
	if err := writeHostDeleteStageJournal(temporary, journal); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(stage, hostDeleteStageJournalName))
}

func (t *Traffic) recordsHaveNoExchanges(ids []string) (bool, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return false, errors.New("invalid traffic deletion journal")
	}
	for _, id := range ids {
		var count int
		if err := t.db.QueryRow(`SELECT count(*) FROM exchanges WHERE id=?`, id).Scan(&count); err != nil {
			return false, err
		}
		if count > 0 {
			return false, nil
		}
	}
	return true, nil
}

// Record journals use the SQLite commit as the recovery decision. Take a write
// fence so another process cannot mistake an in-flight deletion for a rollback.
func (t *Traffic) recoverRecordDeleteStages() error {
	entries, err := os.ReadDir(filepath.Join(t.dir, "_delete_staging"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	found := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(t.dir, "_delete_staging", entry.Name(), hostDeleteStageJournalName))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		var journal hostDeleteStageJournal
		if json.Unmarshal(raw, &journal) == nil && (len(journal.RecordIDs) > 0 || journal.ClearAll) {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	tx, err := t.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE exchanges SET id=id WHERE 0`); err != nil {
		return err
	}
	if err = t.recoverDeleteStages(nil, true); err != nil {
		return err
	}
	return tx.Commit()
}

// FinishCleanup retries only committed file/blob reclamation, never the user's
// original record selection. It is safe after new records have been captured.
func (t *Traffic) FinishCleanup() error {
	t.reaping.Wait()
	if err := t.recoverRecordDeleteStages(); err != nil {
		return err
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	return t.gcBlobs()
}
