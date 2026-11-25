package aicleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/yyhuni/lunafox/server/internal/aitraffic"
)

func validUUID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

func (s *Service) removeFiles(ctx context.Context, job cleanupJob) error {
	if !filepath.IsAbs(s.root) || s.root == "/" || !validUUID(job.TenantID) || job.OwnerUserID <= 0 {
		return ErrInvalid
	}
	var payload cleanupPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	for _, id := range append(append([]string{}, payload.Conversations...), payload.GatherRuns...) {
		if !validUUID(id) {
			return ErrInvalid
		}
	}
	if job.Kind != "conversation" && job.Kind != "task" {
		return ErrInvalid
	}
	if job.Kind == "conversation" && !validUUID(job.ScopeID) {
		return ErrInvalid
	}
	if job.Kind == "conversation" && (len(payload.Conversations) != 1 || payload.Conversations[0] != job.ScopeID) {
		return ErrInvalid
	}
	task, err := strconv.ParseInt(job.ScopeID, 10, 64)
	if job.Kind == "task" && (err != nil || task <= 0 || strconv.FormatInt(task, 10) != job.ScopeID || job.ExplorationID <= 0) {
		return ErrInvalid
	}
	base, err := os.OpenRoot(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer base.Close()
	owner := filepath.Join(job.TenantID, fmt.Sprintf("user-%d", job.OwnerUserID))
	for _, path := range []string{job.TenantID, owner} {
		info, err := base.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("invalid AI storage owner directory")
		}
	}
	root, err := base.OpenRoot(owner)
	if err != nil {
		return err
	}
	defer root.Close()
	remove := func(path string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return root.RemoveAll(path)
	}
	removeTraffic := func(scope aitraffic.Scope, name string) error {
		if s.traffic != nil {
			if err := s.traffic.RemoveScope(ctx, scope, func() error { return remove(filepath.Join("traffic", name)) }); err != nil {
				return err
			}
		} else if err := remove(filepath.Join("traffic", name)); err != nil {
			return err
		}
		return remove(filepath.Join("work", ".traffic-ca", name+".pem"))
	}
	for _, id := range payload.Conversations {
		if err := removeTraffic(aitraffic.Scope{TenantID: job.TenantID, UserID: job.OwnerUserID, ConversationID: id}, "conversation-"+id); err != nil {
			return err
		}
		for _, path := range []string{filepath.Join("work", "sessions", id), filepath.Join("transcripts", id+".jsonl"), filepath.Join("transcripts", id), filepath.Join("work", "noa", id)} {
			if err := remove(path); err != nil {
				return err
			}
		}
	}
	for _, id := range payload.GatherRuns {
		if err := remove(filepath.Join("work", "info-gather", id)); err != nil {
			return err
		}
	}
	if job.Kind == "task" {
		if err := removeTraffic(aitraffic.Scope{TenantID: job.TenantID, UserID: job.OwnerUserID, TaskID: task}, "task-"+job.ScopeID); err != nil {
			return err
		}
		if err := remove(filepath.Join("work", "tasks", job.ScopeID)); err != nil {
			return err
		}
		prefix := fmt.Sprintf("exp%d-", job.ExplorationID)
		for _, dir := range []string{"transcripts", filepath.Join("work", "noa")} {
			file, err := root.Open(dir)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			entries, err := file.ReadDir(-1)
			file.Close()
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), prefix) {
					if err := remove(filepath.Join(dir, entry.Name())); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
