// Native development installs are explicit and separate from remote signed
// Docker releases. This command is never used by production bootstrap.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	build "github.com/yyhuni/lunafox/contracts/enginemanifest/enginepackagebuild"
	catalog "github.com/yyhuni/lunafox/contracts/enginemanifest/packagecatalog"
	"github.com/yyhuni/lunafox/contracts/ociartifact"
	"github.com/yyhuni/lunafox/server/internal/config"
	"github.com/yyhuni/lunafox/server/internal/database"
	"github.com/yyhuni/lunafox/server/internal/engineinstall"
	repository "github.com/yyhuni/lunafox/server/internal/modules/catalog/repository"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "local Engine package update:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: local-engine-bootstrap <engine-source-directory>")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !cfg.LocalMode {
		return fmt.Errorf("native Engine packages require TIANQIONG_LOCAL_MODE=true")
	}
	db, err := database.NewDatabase(&cfg.Database)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	var busy int64
	if err := db.Raw(`SELECT count(*) FROM scan_task WHERE status='running'`).Scan(&busy).Error; err != nil {
		return err
	}
	if busy > 0 {
		return fmt.Errorf("running scan tasks must finish before updating native Engine packages")
	}
	repo := repository.NewEngineRepository(db)
	engines, err := repo.ListInstalledEngines()
	if err != nil {
		return err
	}
	cache := engineinstall.CacheInstaller{Root: cfg.Storage.EnginePackageCacheRoot, MaxArchiveBytes: cfg.EngineInstall.MaxPackageBytes}
	for _, engine := range engines {
		name := strings.TrimPrefix(engine.EngineID, "engine.lunafox.")
		if name == engine.EngineID || strings.ContainsAny(name, "/\\.") {
			continue
		}
		source := filepath.Join(os.Args[1], name)
		if _, err := os.Stat(filepath.Join(source, "engine.json")); os.IsNotExist(err) {
			continue
		}
		current, err := cache.LoadExactPackage(ociartifact.PackageDigest(engine.PackageDigest))
		if err != nil {
			return err
		}
		paths := []string{"engine.json", "locales/en.json", "locales/zh.json"}
		entries := make([]catalog.PackageLayoutEntry, 0, 4)
		hash := sha256.New()
		changed := false
		for _, path := range paths {
			data, err := os.ReadFile(filepath.Join(source, path))
			if err != nil {
				return err
			}
			old, err := os.ReadFile(filepath.Join(current.RootPath, path))
			if err != nil {
				return err
			}
			if !bytes.Equal(data, old) {
				changed = true
			}
			hash.Write([]byte(path + "\x00"))
			hash.Write(data)
			entries = append(entries, catalog.PackageLayoutEntry{Path: path, Mode: 0o644, Payload: data})
		}
		if !changed {
			continue
		}
		manifest := current.Layout.Definition.PackageManifest
		manifest.EngineVersion = "0.0.0+native." + hex.EncodeToString(hash.Sum(nil))
		packageJSON, err := json.Marshal(manifest)
		if err != nil {
			return err
		}
		entries = append(entries, catalog.PackageLayoutEntry{Path: "package.json", Mode: 0o644, Payload: packageJSON})
		var archive bytes.Buffer
		digest, size, err := build.WriteEnginePackageArchive(&archive, entries)
		if err != nil {
			return err
		}
		if _, err := cache.StageAndPromotePackage(context.Background(), bytes.NewReader(archive.Bytes()), digest, size, func(_ context.Context, layout catalog.EnginePackageLayout) error {
			if layout.Definition.EngineDefinition.EngineID != engine.EngineID {
				return fmt.Errorf("native Engine identity mismatch")
			}
			return nil
		}); err != nil {
			return err
		}
		// The native reference records a local artifact identity and is never a
		// claim that these bytes were published to the signed Docker inventory.
		artifactHash := sha256.Sum256(append([]byte("skyshield.native-engine.v1\n"), archive.Bytes()...))
		engine.ArtifactRef = "native.invalid/skyshield/" + strings.ReplaceAll(name, "_", "-") + "@sha256:" + hex.EncodeToString(artifactHash[:])
		engine.PackageDigest = string(digest)
		engine.PackageVersion = manifest.EngineVersion
		engine.Manifest = entries[0].Payload
		if err := repo.UpsertInstalledEngine(context.Background(), &engine, true); err != nil {
			return err
		}
		fmt.Println("updated native Engine:", engine.EngineID)
	}
	return nil
}
