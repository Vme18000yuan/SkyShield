package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	settings "github.com/yyhuni/lunafox/server/internal/modules/aisettings/application"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"gorm.io/gorm"
)

// Hashes identify the original shipped files, not a user's skill document or
// configuration. The complete group is upgraded only when none was edited.
var infoGatherOriginalScripts = map[string]string{
	"scripts/recon_common.py":       "0b871a32376ff22a398d6bbfb9e8c6c7121150d24f47fa51d3174d49079bd846",
	"scripts/batch_recon.py":        "94d9311e6fcf68f376e74e0f80fa6850b8585dd2d6ffb8b291a348192504432d",
	"scripts/find_real_ip.sh":       "0b1a3b14cb25692786288287373c93745f7a6f0c34cefd923b39d729712f02ea",
	"scripts/scan_ports.py":         "b376e95863522f960dc2894e2cf939a1046e9ac87d595c8934b70b864ac644e3",
	"scripts/fingerprint_driver.py": "2fdc6e9ec2937c2416a039a9e5f6a74b316e8983e86f8a5e50a076a898f89994",
	"scripts/web_probe_driver.py":   "abcee773216a0d6c5c5141ac34f1d615e72bd07c63149d4b46cf2a9804b73251",
	"scripts/recon_pipeline.py":     "15a6ba187771cd43fe814ba77f7929ae7a29f9dae5449ae221a86daf7a680243",
	"scripts/recon_supervisor.py":   "",
	"scripts/web_batch.py":          "",
}

// The phase adapter depends on this version of the pipeline's public methods.
// If an administrator customized that script, leave the package untouched.
var infoGatherAgenticScripts = map[string]string{
	"scripts/recon_pipeline.py": "a563625753f28786b560f807f994e9164ed5ff22a4316419d3d078c4ccbf4c60",
	"scripts/agentic_recon.py":  "",
}

var infoGatherResumeScripts = map[string]string{
	"scripts/recon_pipeline.py": "9991a5033e2ddd51c2c639af94ab73bb27c25a559a60f93911035811327bba04",
	"scripts/agentic_recon.py":  "8dee858867d5da962f6413edb44c689d88ebeb611cd9615e9d96fa294912e2fc",
}

var infoGatherRiskContract = map[string]string{
	"references/skyshield-import.schema.json": "53d1f0c5561167f2f1b71ed804660747cb5e25a02c1e888ca17878da4ec55066",
}

func planInfoGatherUpgrade(current, desired map[string]settings.SkillFile, original map[string]string) ([]settings.SkillFile, string) {
	updates := make([]settings.SkillFile, 0, len(original))
	for path, oldHash := range original {
		next, ok := desired[path]
		if !ok || next.Directory {
			return nil, path
		}
		for existingPath, existing := range current {
			if strings.HasPrefix(path, existingPath+"/") && !existing.Directory || strings.HasPrefix(existingPath, path+"/") {
				return nil, path
			}
		}
		existing, present := current[path]
		if !present {
			if oldHash != "" {
				return nil, path
			}
			updates = append(updates, next)
			continue
		}
		if existing.Directory {
			return nil, path
		}
		if bytes.Equal(existing.Content, next.Content) {
			continue
		}
		digest := sha256.Sum256(existing.Content)
		if oldHash == "" || hex.EncodeToString(digest[:]) != oldHash {
			return nil, path
		}
		next.Mode = existing.Mode
		updates = append(updates, next)
	}
	return updates, ""
}

func (s *SkillStore) upgradeInfoGatherScripts(ctx context.Context) error {
	return s.upgradeInfoGatherScriptsWithBaseline(ctx, infoGatherOriginalScripts)
}

func (s *SkillStore) upgradeInfoGatherScriptsWithBaseline(ctx context.Context, original map[string]string) error {
	return s.upgradeInfoGatherScriptsVersion(ctx, original, "builtin_info_gather_coverage_fix_v1/")
}

func (s *SkillStore) upgradeInfoGatherAgenticScripts(ctx context.Context) error {
	return s.upgradeInfoGatherScriptsVersion(ctx, infoGatherAgenticScripts, "builtin_info_gather_agentic_v1/")
}

func (s *SkillStore) upgradeInfoGatherResumeScripts(ctx context.Context) error {
	return s.upgradeInfoGatherScriptsVersion(ctx, infoGatherResumeScripts, "builtin_info_gather_resume_v1/")
}

func (s *SkillStore) upgradeInfoGatherRiskContract(ctx context.Context) error {
	return s.upgradeInfoGatherScriptsVersion(ctx, infoGatherRiskContract, "builtin_info_gather_risk_contract_v1/")
}

func (s *SkillStore) upgradeInfoGatherScriptsVersion(ctx context.Context, original map[string]string, flagPrefix string) error {
	if s.builtinRoot == "" {
		return nil
	}
	root, err := os.OpenRoot(s.builtinRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	desired := make(map[string]settings.SkillFile, len(original))
	for name := range original {
		if !settings.ValidSkillFilePath(name) {
			return settings.ErrInvalidProfile
		}
		file, openErr := root.Open(filepath.Join("info-gath", filepath.FromSlash(name)))
		if openErr != nil {
			slog.Warn("信息收集内置脚本不可用，已跳过自动升级", "path", name, "error", openErr)
			return nil
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > 20<<20 {
			file.Close()
			slog.Warn("信息收集内置脚本无效，已跳过自动升级", "path", name)
			return nil
		}
		content, readErr := io.ReadAll(io.LimitReader(file, (20<<20)+1))
		file.Close()
		if readErr != nil || len(content) > 20<<20 {
			slog.Warn("信息收集内置脚本无法读取，已跳过自动升级", "path", name)
			return nil
		}
		desired[name] = settings.SkillFile{Path: name, Content: content, Mode: int(info.Mode().Perm())}
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAgentSettingsTenant(tx); err != nil {
			return err
		}
		var id string
		err := tx.Raw(`SELECT id::text FROM ai_skill WHERE tenant_id=? AND builtin_key='info-gath' FOR UPDATE`, assets.DefaultTenantID).Row().Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		flag := flagPrefix + id
		var done bool
		if err = tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_runtime_seed WHERE tenant_id=? AND seed_key=?)`, assets.DefaultTenantID, flag).Scan(&done).Error; err != nil || done {
			return err
		}
		files, err := readSkillPackageFiles(tx, id)
		if err != nil {
			return err
		}
		current := make(map[string]settings.SkillFile, len(files))
		for _, file := range files {
			current[file.Path] = file
		}
		updates, conflict := planInfoGatherUpgrade(current, desired, original)
		if conflict != "" {
			slog.Warn("信息收集 Skill 包含自定义脚本，已跳过自动升级", "path", conflict)
			return nil
		}
		for _, file := range updates {
			if err := writeSkillFile(tx, id, file); err != nil {
				return err
			}
		}
		return tx.Exec(`INSERT INTO ai_runtime_seed(tenant_id,seed_key) VALUES(?,?) ON CONFLICT DO NOTHING`, assets.DefaultTenantID, flag).Error
	})
}

// Upgrade only the shipped evidence scripts; preserve administrator edits.
var infoGatherEvidenceScripts = map[string]string{
	"scripts/recon_report.py":     "c6451dc7d118ef8f95eaadb3b6151876c2ee82fe5b08fa1aa1f9491ecdacbc99",
	"scripts/skyshield_output.py": "450997a63fd27053914eec003d809f328b9e4dcef12abe377df9247f4e6b6440",
	"scripts/web_analysis.py":     "765f7297d57b61b36eda43eeeb6baa3496417fd0c75d0adcf1efe5c302e403c4",
}

func (s *SkillStore) upgradeInfoGatherEvidenceScripts(ctx context.Context) error {
	return s.upgradeInfoGatherScriptsVersion(ctx, infoGatherEvidenceScripts, "builtin_info_gather_evidence_v1/")
}
