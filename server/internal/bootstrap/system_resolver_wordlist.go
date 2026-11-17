package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	catalogrepo "github.com/yyhuni/lunafox/server/internal/modules/catalog/repository"
	"gorm.io/gorm"
)

type managedDNSState struct {
	Applied string `json:"applied"`
	Pending string `json:"pending,omitempty"`
}

func dnsContentHash(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func writeDNSFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".skyshield-dns-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// Only the unchanged packaged default, or our previously generated content, is
// managed. A user edit transfers ownership back to the user and is never reset.
func resolverContentIsManaged(actual, seed string, state *managedDNSState) bool {
	if state == nil {
		return actual == seed
	}
	return actual == state.Applied || state.Pending != "" && actual == state.Pending
}

func syncSystemResolverWordlist(ctx context.Context, db *gorm.DB, basePath string, imports []defaultWordlistImport, desired string) error {
	seed := ""
	for _, item := range imports {
		if item.entry.FileName == "resolvers.txt" {
			seed = item.fileHash
		}
	}
	if seed == "" {
		return nil
	}
	marker := filepath.Join(basePath, ".skyshield-system-dns.json")
	applied := ""
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec(`SELECT pg_advisory_xact_lock(?)`, int64(0x534b59444e53)).Error; err != nil {
				return err
			}
		}
		rows, err := catalogrepo.NewWordlistRepository(tx).ListAllContext(ctx)
		if err != nil {
			return err
		}
		for _, wordlist := range rows {
			if wordlist.FileName != "resolvers.txt" {
				continue
			}
			path := filepath.Join(basePath, "resolvers.txt")
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("default resolver file is not a regular file")
			}
			if wordlist.FilePath != path || info.Size() > 64<<10 {
				return nil
			}
			previous, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var state *managedDNSState
			if info, err := os.Lstat(marker); err == nil {
				if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
					return fmt.Errorf("managed DNS state must be a private regular file")
				}
				payload, err := os.ReadFile(marker)
				if err != nil {
					return err
				}
				state = &managedDNSState{}
				if err := json.Unmarshal(payload, state); err != nil {
					return fmt.Errorf("managed DNS state is invalid")
				}
			} else if !os.IsNotExist(err) {
				return err
			}
			actual := dnsContentHash(previous)
			if !resolverContentIsManaged(actual, seed, state) {
				return nil
			}
			next := dnsContentHash([]byte(desired))
			if actual == next && wordlist.FileHash == next && wordlist.FileSize == int64(len(desired)) && wordlist.LineCount == strings.Count(desired, "\n") {
				if state == nil || state.Applied != next || state.Pending != "" {
					applied = next
				}
				return nil
			}
			journal, _ := json.Marshal(managedDNSState{Applied: actual, Pending: next})
			if err := writeDNSFile(marker, journal, 0o600); err != nil {
				return err
			}
			if actual != next {
				if err := writeDNSFile(path, []byte(desired), info.Mode().Perm()); err != nil {
					return err
				}
			}
			wordlist.UpdateFileStats(int64(len(desired)), strings.Count(desired, "\n"), next)
			if err := catalogrepo.NewWordlistRepository(tx).Update(&wordlist); err != nil {
				return errors.Join(err, writeDNSFile(path, previous, info.Mode().Perm()))
			}
			// The pending hash survives a crash between file and DB publication, so
			// a later bootstrap can reconcile metadata without mistaking it for a user edit.
			applied = next
			return nil
		}
		return fmt.Errorf("default resolver catalog entry is missing")
	})
	if err != nil {
		return err
	}
	if applied != "" {
		payload, _ := json.Marshal(managedDNSState{Applied: applied})
		return writeDNSFile(marker, payload, 0o600)
	}
	return nil
}
