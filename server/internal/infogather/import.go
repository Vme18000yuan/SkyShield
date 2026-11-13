package infogather

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	assetdomain "github.com/yyhuni/lunafox/server/internal/modules/asset/domain"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	securityapp "github.com/yyhuni/lunafox/server/internal/modules/security/application"
	"github.com/yyhuni/lunafox/server/internal/pkg/dbtx"
	"gorm.io/gorm"
)

type RecordRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type ImportFailure struct {
	Record RecordRef `json:"record"`
	Reason string    `json:"reason"`
}
type ImportResult struct {
	Imported int             `json:"imported"`
	Failed   int             `json:"failed"`
	Failures []ImportFailure `json:"failures"`
}

const duplicateRiskReason = "已归集为对应的漏洞或风险"

// SyncSelected is only called by the authenticated, explicit review action.
// The caller cannot make an observation match a target by changing its hint.
func (s *Store) SyncSelected(ctx context.Context, user int, runID string, targetID int, refs []RecordRef, allowCDN bool) (ImportResult, error) {
	if len(refs) == 0 || len(refs) > 100 || targetID <= 0 {
		return ImportResult{}, errors.New("请选择目标及 1 至 100 条待处理记录")
	}
	return s.sync(ctx, user, runID, targetID, refs, allowCDN, false)
}

func (s *Store) syncAutomatic(ctx context.Context, user int, runID string, targetID int) (ImportResult, error) {
	run, err := s.Run(ctx, user, runID)
	if err != nil {
		return ImportResult{}, err
	}
	if err = s.authorize(ctx, user, activeExecution(run)); err != nil {
		return ImportResult{}, err
	}
	rows, err := s.db.WithContext(ctx).Raw(`SELECT r.kind,r.record_id FROM ai_info_gather_record r WHERE r.run_id=?::uuid AND r.status='pending' AND (
		r.kind IN ('asset','vulnerability') OR (r.kind='observation' AND r.payload->>'category' IN ('ANONYMOUS_DATA','REVIEW_TOKEN_DIFFERENCE','PUBLIC_LISTING','DNS_ZONE_TRANSFER') AND NOT EXISTS (
			SELECT 1 FROM ai_info_gather_record v WHERE v.run_id=r.run_id AND v.kind='vulnerability' AND v.record_id=r.record_id AND v.status IN ('pending','imported')
			AND v.payload->>'type'=r.payload->>'category' AND v.payload->>'url'=r.payload->>'value'
		))) ORDER BY CASE r.kind WHEN 'asset' THEN 0 WHEN 'vulnerability' THEN 1 ELSE 2 END,r.record_id`, runID).Rows()
	if err != nil {
		return ImportResult{}, err
	}
	refs := []RecordRef{}
	for rows.Next() {
		var ref RecordRef
		if err = rows.Scan(&ref.Kind, &ref.ID); err != nil {
			rows.Close()
			return ImportResult{}, err
		}
		refs = append(refs, ref)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return ImportResult{}, err
	}
	rows.Close()
	return s.sync(ctx, user, runID, targetID, refs, false, true)
}

func (s *Store) sync(ctx context.Context, user int, runID string, targetID int, refs []RecordRef, allowCDN, automatic bool) (ImportResult, error) {
	result := ImportResult{Failures: []ImportFailure{}}
	run, err := s.Run(ctx, user, runID)
	if err != nil {
		return result, err
	}
	if run.Status == "running" {
		return result, errors.New("采集尚未结束")
	}
	if automatic {
		if err = s.authorize(ctx, user, activeExecution(run)); err != nil {
			return result, err
		}
	}
	seen := map[RecordRef]bool{}
	for _, ref := range refs {
		if (ref.Kind != "asset" && ref.Kind != "vulnerability" && ref.Kind != "observation") || !recordIDPattern.MatchString(ref.ID) || seen[ref] {
			return result, errors.New("待处理记录无效或重复")
		}
		seen[ref] = true
	}
	refs = append([]RecordRef(nil), refs...)
	priority := map[string]int{"asset": 0, "vulnerability": 1, "observation": 2}
	sort.SliceStable(refs, func(i, j int) bool { return priority[refs[i].Kind] < priority[refs[j].Kind] })
	for _, ref := range refs {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if automatic {
			if err = s.authorize(ctx, user, activeExecution(run)); err != nil {
				return result, err
			}
		}
		imported, reason, err := s.importOne(ctx, user, run, ref, targetID, allowCDN, automatic)
		if err != nil {
			return result, err
		}
		if !imported && ref.Kind == "observation" && reason == duplicateRiskReason {
			continue
		}
		if imported {
			result.Imported++
		} else {
			result.Failed++
			if len(result.Failures) < 50 {
				result.Failures = append(result.Failures, ImportFailure{ref, reason})
			}
			if len(reason) > 1000 {
				reason = reason[:1000]
			}
			if err = s.db.WithContext(ctx).Exec(`UPDATE ai_info_gather_record SET reason=?,updated_at=clock_timestamp() WHERE run_id=?::uuid AND kind=? AND record_id=? AND status='pending'`, reason, runID, ref.Kind, ref.ID).Error; err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func (s *Store) importOne(ctx context.Context, user int, run Run, ref RecordRef, targetID int, allowCDN, automatic bool) (bool, string, error) {
	var reason string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var role string
		err := tx.Raw(`SELECT m.role FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE m.tenant_id=?::uuid AND m.user_id=? AND t.status='active' AND u.is_active FOR SHARE OF m,u`, assetapp.DefaultTenantID, user).Row().Scan(&role)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && role != "admin" && role != "analyst") {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		var runStatus string
		if err = tx.Raw(`SELECT status FROM ai_info_gather_run WHERE id=?::uuid AND tenant_id=?::uuid AND owner_user_id=? FOR SHARE`, run.ID, assetapp.DefaultTenantID, user).Row().Scan(&runStatus); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if runStatus == "running" {
			return errors.New("采集尚未结束")
		}
		var target assetdomain.TargetRef
		err = tx.Raw(`SELECT id,name,type FROM target WHERE id=? AND deleted_at IS NULL FOR UPDATE`, targetID).Row().Scan(&target.ID, &target.Name, &target.Type)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("所选目标不存在或已删除")
		}
		if err != nil {
			return err
		}
		if automatic && !targetContainsScope(target, run.scope()) {
			return errors.New("采集范围不属于所选目标")
		}
		var payload []byte
		var state, currentReason string
		err = tx.Raw(`SELECT r.payload,r.status,r.reason FROM ai_info_gather_record r JOIN ai_info_gather_run g ON g.id=r.run_id WHERE r.run_id=?::uuid AND g.tenant_id=?::uuid AND g.owner_user_id=? AND r.kind=? AND r.record_id=? FOR UPDATE OF r`, run.ID, assetapp.DefaultTenantID, user, ref.Kind, ref.ID).Row().Scan(&payload, &state, &currentReason)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if state != "pending" {
			reason = "记录已处理"
			if ref.Kind == "observation" && state == "ignored" && currentReason == duplicateRiskReason {
				reason = duplicateRiskReason
			}
			return nil
		}
		txCtx := dbtx.WithTransaction(ctx, tx)
		recordStatus, recordReason := "imported", ""
		if ref.Kind == "asset" {
			var value Asset
			if err = json.Unmarshal(payload, &value); err != nil {
				return err
			}
			if automatic {
				if _, err = toAssetInput(value, run.scope().target(), false, run.ID); err != nil {
					reason = err.Error()
					return nil
				}
			}
			input, err := toAssetInput(value, target, allowCDN, run.ID)
			if err != nil {
				reason = err.Error()
				return nil
			}
			origin, request := "import", "info-gath/"+run.ID+"/"+ref.ID
			if automatic {
				origin, request = "ai", activeExecution(run)+"/info-gath/"+run.ID+"/"+ref.ID
			}
			registered, err := s.assets.Register(txCtx, assetapp.BatchRegisterInput{UserID: user, Origin: origin, RequestID: request, Assets: []assetapp.AssetInput{input}})
			if err != nil {
				return fmt.Errorf("资产登记失败: %w", err)
			}
			if len(registered.Errors) > 0 {
				reason = registered.Errors[0].Error
				return nil
			}
			if len(registered.Results) != 1 {
				return errors.New("资产登记未返回结果")
			}
		} else {
			risk, err := decodeRisk(ref.Kind, payload)
			if err != nil {
				reason = err.Error()
				return nil
			}
			if !risk.DNS && unsupportedIPv6URL(risk.Address) {
				reason = "系统暂不支持 IPv6 URL 风险，记录已保留"
				return nil
			}
			asset := Asset{Type: "endpoint", Value: risk.Address, Scope: "in_scope"}
			if risk.DNS {
				asset.Type = "domain"
			}
			if automatic {
				if _, err = toAssetInput(asset, run.scope().target(), false, run.ID); err != nil {
					reason = "风险地址不在本次采集范围内"
					return nil
				}
			}
			input, err := toAssetInput(asset, target, false, run.ID)
			if err != nil {
				reason = "风险地址不属于所选目标，请选择对应目标"
				return nil
			}
			if s.assets == nil || s.risks == nil {
				return errors.New("资产或风险登记服务不可用")
			}
			origin, request := "import", "info-gath/"+run.ID+"/risk-asset/"+ref.ID
			if automatic {
				origin, request = "ai", activeExecution(run)+"/info-gath/"+run.ID+"/risk-asset/"+ref.ID
			}
			registered, err := s.assets.Register(txCtx, assetapp.BatchRegisterInput{UserID: user, Origin: origin, RequestID: request, Assets: []assetapp.AssetInput{input}})
			if err != nil {
				return fmt.Errorf("风险关联资产登记失败: %w", err)
			}
			if len(registered.Errors) > 0 {
				reason = registered.Errors[0].Error
				return nil
			}
			if len(registered.Results) != 1 {
				return errors.New("风险关联资产未返回结果")
			}
			assetID, err := strconv.ParseInt(registered.Results[0].ID, 10, 64)
			if err != nil || assetID <= 0 {
				return errors.New("风险关联资产标识无效")
			}
			item := securityapp.VulnerabilityCreateItem{URL: risk.Address, VulnType: risk.Title, Severity: "info", Source: "info-gath", Description: risk.Description,
				RawOutput: map[string]any{"runId": run.ID, "recordId": ref.ID, "evidence": risk.Evidence, "status": "needs_review", "confidence": "candidate"}}
			dbURL := risk.Address
			if risk.DNS {
				dns, ok := s.risks.(interface {
					CreateDNSRiskContext(context.Context, int, string, securityapp.VulnerabilityCreateItem) (int64, error)
				})
				if !ok {
					return errors.New("DNS 风险登记服务不可用")
				}
				item.URL = ""
				_, err = dns.CreateDNSRiskContext(txCtx, target.ID, risk.Address, item)
				dbURL = "dns://" + risk.Address
			} else {
				writer, ok := s.risks.(interface {
					CreateInfoGatherRiskContext(context.Context, int, securityapp.VulnerabilityCreateItem) (int64, error)
				})
				if !ok {
					return errors.New("风险登记服务不可用")
				}
				_, err = writer.CreateInfoGatherRiskContext(txCtx, target.ID, item)
			}
			if err != nil {
				return fmt.Errorf("风险登记失败: %w", err)
			}
			link := tx.Exec(`UPDATE vulnerability SET asset_id=COALESCE(asset_id,?) WHERE target_id=? AND url=? AND vuln_type=? AND severity='info'`, assetID, target.ID, dbURL, risk.Title)
			if link.Error != nil {
				return link.Error
			}
			if link.RowsAffected != 1 {
				return errors.New("风险资产关联失败")
			}
			if ref.Kind == "vulnerability" {
				if err := tx.Exec(`UPDATE ai_info_gather_record SET status='ignored',target_id=?,reason='已归集为对应的漏洞或风险',updated_at=clock_timestamp() WHERE run_id=?::uuid AND kind='observation' AND record_id=? AND status='pending' AND payload->>'category'=? AND payload->>'value'=?`, target.ID, run.ID, ref.ID, risk.Category, risk.Address).Error; err != nil {
					return err
				}
			} else {
				var counterpart bool
				if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM ai_info_gather_record WHERE run_id=?::uuid AND kind='vulnerability' AND record_id=? AND status IN ('pending','imported') AND payload->>'type'=? AND payload->>'url' IN (?,?))`, run.ID, ref.ID, risk.Category, risk.Address, dbURL).Scan(&counterpart).Error; err != nil {
					return err
				}
				if counterpart {
					if err := tx.Exec(`UPDATE ai_info_gather_record SET status='imported',target_id=?,reason='',updated_at=clock_timestamp() WHERE run_id=?::uuid AND kind='vulnerability' AND record_id=? AND status='pending' AND payload->>'type'=? AND payload->>'url' IN (?,?)`, target.ID, run.ID, ref.ID, risk.Category, risk.Address, dbURL).Error; err != nil {
						return err
					}
					recordStatus, recordReason = "ignored", duplicateRiskReason
				}
			}
		}
		return tx.Exec(`UPDATE ai_info_gather_record SET status=?,target_id=?,reason=?,updated_at=clock_timestamp() WHERE run_id=?::uuid AND kind=? AND record_id=?`, recordStatus, target.ID, recordReason, run.ID, ref.Kind, ref.ID).Error
	})
	if err != nil {
		if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) || ctx.Err() != nil {
			return false, "", err
		}
		// Return a bounded reason without leaking database internals or credentials.
		if strings.Contains(err.Error(), "所选目标不存在") || strings.Contains(err.Error(), "采集范围") {
			return false, err.Error(), nil
		}
		return false, "导入失败，请检查目标状态和服务日志后重试", nil
	}
	if reason != "" {
		return false, reason, nil
	}
	return true, "", nil
}
