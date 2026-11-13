package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/google/uuid"
	contractresults "github.com/yyhuni/lunafox/contracts/results"
	"golang.org/x/net/publicsuffix"
)

type RegistrationFacade struct {
	store  RegistrationStore
	codec  AssetSecretCodec
	aiGate AIAssetGate
}

// AIAssetGate evaluates the ARTEX asset rules for one trusted agent run.
// It is set during bootstrap and does not apply to manual/import registration.
type AIAssetGate func(context.Context, int, *RegistrationTaskScope, string, []AssetInput) ([]string, error)

func (f *RegistrationFacade) SetAIAssetGate(gate AIAssetGate) { f.aiGate = gate }

func NewRegistrationFacade(store RegistrationStore, codec AssetSecretCodec) *RegistrationFacade {
	return &RegistrationFacade{store: store, codec: codec}
}

func (f *RegistrationFacade) Register(ctx context.Context, input BatchRegisterInput) (*BatchRegisterResult, error) {
	if ctx == nil || input.UserID <= 0 || len(input.Assets) == 0 || len(input.Assets) > 100 || len(input.RequestID) > 300 {
		return nil, ErrInvalidAsset
	}
	if input.Origin != "manual" && input.Origin != "ai" && input.Origin != "import" {
		return nil, ErrInvalidAsset
	}
	if input.Task != nil && (input.Origin != "ai" || input.Task.TaskID <= 0 || input.Task.ExplorationID <= 0 || input.Task.IntentID < 0 || input.Task.ExecutionID == "") {
		return nil, ErrInvalidAsset
	}
	if input.RequestID == "" {
		input.RequestID = uuid.NewString()
	}
	var blocked []string
	if input.Origin == "ai" && f.aiGate != nil {
		var err error
		blocked, err = f.aiGate(ctx, input.UserID, input.Task, input.RequestID, input.Assets)
		if err != nil || len(blocked) != len(input.Assets) {
			return nil, fmt.Errorf("资产拦截规则不可用")
		}
	}
	result := &BatchRegisterResult{RequestID: input.RequestID, Results: []AssetRegistrationResult{}, Errors: []AssetRegistrationError{}}
	for index, item := range input.Assets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(blocked) > 0 && blocked[index] != "" {
			result.Errors = append(result.Errors, AssetRegistrationError{Index: index, Error: "资产" + blocked[index] + "，已禁止插入"})
			continue
		}
		prepared, err := prepareRegistration(item)
		if err != nil {
			result.Errors = append(result.Errors, AssetRegistrationError{Index: index, Error: err.Error()})
			continue
		}
		raw, err := json.Marshal(item)
		if err != nil || len(raw) > 64*1024 {
			result.Errors = append(result.Errors, AssetRegistrationError{Index: index, Error: "资产字段过大或格式无效"})
			continue
		}
		sourceKey := fmt.Sprintf("user-%d/%s/%d", input.UserID, input.RequestID, index)
		scope := DefaultTenantID + "/" + input.Origin + "/" + sourceKey
		digest := sha256.Sum256(raw)
		fingerprint := "sha256:" + hex.EncodeToString(digest[:])
		var encrypted []byte
		if len(item.Auth) > 0 {
			if f.codec == nil {
				result.Errors = append(result.Errors, AssetRegistrationError{Index: index, Error: "凭据加密未配置"})
				continue
			}
			auth, encodeErr := json.Marshal(item.Auth)
			if encodeErr != nil {
				result.Errors = append(result.Errors, AssetRegistrationError{Index: index, Error: "认证字段无效"})
				continue
			}
			encrypted, err = f.codec.Seal(scope, string(auth))
			if err == nil {
				fingerprint, err = f.codec.Fingerprint(scope, raw)
			}
			if err != nil {
				result.Errors = append(result.Errors, AssetRegistrationError{Index: index, Error: "凭据加密不可用"})
				continue
			}
		}
		clean := prepared.Input
		clean.Auth = nil
		payload, _ := json.Marshal(clean)
		prepared.Input.Auth = nil
		asset, err := f.store.RegisterAsset(ctx, RegistrationCommand{UserID: input.UserID, Asset: prepared, Origin: input.Origin,
			SourceKey: sourceKey, Fingerprint: fingerprint, Payload: payload, EncryptedAuth: encrypted, Task: input.Task})
		if err != nil {
			message := "资产登记失败"
			switch {
			case errors.Is(err, ErrAssetWriteForbidden):
				message = "当前账号无资产写入权限"
			case errors.Is(err, ErrAssetTargetNotFound):
				message = "目标不存在或已删除"
			case errors.Is(err, ErrAssetOutsideTarget):
				message = "资产不在所选目标范围内"
			case errors.Is(err, ErrAssetRequestConflict):
				message = "请求标识已用于不同的资产内容"
			}
			result.Errors = append(result.Errors, AssetRegistrationError{Index: index, Error: message})
			continue
		}
		result.Results = append(result.Results, AssetRegistrationResult{Index: index, ID: strconv.FormatInt(asset.ID, 10), Type: asset.Type, Projection: asset.Projection})
	}
	return result, nil
}

func prepareRegistration(input AssetInput) (PreparedAsset, error) {
	p := PreparedAsset{Input: input, Projection: "extended"}
	if input.TargetID < 0 || input.OrganizationID < 0 || len(input.RecordValue) > 100 || len(input.OpenPorts) > 100 || len(input.Params) > 100 || len(input.Auth) > 100 || len(input.BoundDomains) > 100 || len(input.Technologies) > 100 {
		return p, ErrInvalidAsset
	}
	if input.Type == "app" && len(input.BoundDomains) > 0 {
		domains := make([]string, 0, len(input.BoundDomains))
		for _, raw := range input.BoundDomains {
			domain, ok := contractresults.NormalizeSubdomainDNSName(raw)
			if !ok {
				return p, fmt.Errorf("关联域名无效")
			}
			domains = append(domains, domain)
		}
		input.BoundDomains = domains
		p.Input.BoundDomains = domains
	}
	if input.ServiceIP != "" {
		if _, err := netip.ParseAddr(input.ServiceIP); err != nil {
			return p, fmt.Errorf("serviceIp 不是有效的 IP 地址")
		}
	}
	for _, port := range input.OpenPorts {
		if port.Port < 1 || port.Port > 65535 {
			return p, fmt.Errorf("端口必须介于 1 和 65535")
		}
	}
	var identity []any
	switch input.Type {
	case "root_domain", "subdomain":
		domain, ok := contractresults.NormalizeSubdomainDNSName(input.Domain)
		if !ok {
			return p, fmt.Errorf("域名无效")
		}
		if input.Type == "root_domain" {
			if root, err := publicsuffix.EffectiveTLDPlusOne(domain); err != nil || root != domain {
				return p, fmt.Errorf("请输入可注册的根域名")
			}
		}
		p.Label = domain
		identity = []any{input.Type, domain, ""}
		if input.Type == "subdomain" && input.RecordType != "" {
			rr := strings.ToUpper(strings.TrimSpace(input.RecordType))
			if len(rr) == 0 || len(rr) > 30 || strings.IndexFunc(rr, func(r rune) bool { return !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') }) >= 0 {
				return p, fmt.Errorf("DNS 记录类型无效")
			}
			p.RecordType = &rr
			identity = []any{input.Type, domain, "", rr}
		}
		if input.Type == "subdomain" && input.TargetID > 0 {
			p.Projection = "subdomain"
		}
	case "ip":
		ip, err := netip.ParseAddr(strings.TrimSpace(input.IP))
		if err != nil {
			return p, fmt.Errorf("IP 地址无效")
		}
		p.Label = ip.Unmap().String()
		identity = []any{input.Type, p.Label, ""}
	case "app":
		p.Label = strings.TrimSpace(input.AppName)
		if p.Label == "" {
			return p, fmt.Errorf("应用名称不能为空")
		}
		identity = []any{input.Type, p.Label, ""}
		if input.BundleID != "" {
			platform := strings.TrimSpace(input.Platform)
			if platform == "" {
				platform = "unknown"
			}
			identity = []any{input.Type, "bundle", platform, strings.TrimSpace(input.BundleID)}
		}
	case "service":
		if input.URL != "" {
			if _, err := contractresults.ValidateObservedAssetURL(input.URL); err != nil {
				return p, fmt.Errorf("服务 URL 无效")
			}
			p.Label = input.URL
			identity = []any{input.Type, input.URL, ""}
			if input.TargetID > 0 {
				p.Projection = "website"
			}
		} else {
			if input.Port < 1 || input.Port > 65535 || strings.TrimSpace(input.ServiceName) == "" {
				return p, fmt.Errorf("非 HTTP 服务需要服务名称和有效端口")
			}
			domain := ""
			ip := ""
			if input.Domain != "" {
				var ok bool
				domain, ok = contractresults.NormalizeSubdomainDNSName(input.Domain)
				if !ok {
					return p, fmt.Errorf("服务域名无效")
				}
			}
			if input.IP != "" {
				parsed, err := netip.ParseAddr(strings.TrimSpace(input.IP))
				if err != nil {
					return p, fmt.Errorf("服务 IP 无效")
				}
				ip = parsed.Unmap().String()
			}
			if domain == "" && ip == "" {
				return p, fmt.Errorf("非 HTTP 服务需要域名或 IP")
			}
			host := domain
			if host == "" {
				host = ip
			}
			p.Label = net.JoinHostPort(host, strconv.Itoa(input.Port))
			protocol := strings.ToLower(strings.TrimSpace(input.Proto))
			if protocol == "" {
				protocol = "unknown"
			}
			if protocol != "tcp" && protocol != "udp" && protocol != "unknown" {
				return p, fmt.Errorf("传输协议必须为 tcp、udp 或 unknown")
			}
			identity = []any{input.Type, "other", domain, ip, input.Port, protocol, strings.ToLower(strings.TrimSpace(input.ServiceName))}
		}
	case "endpoint":
		if _, err := contractresults.ValidateObservedAssetURL(input.URL); err != nil {
			return p, fmt.Errorf("接口 URL 无效")
		}
		p.Label = input.URL
		method := strings.ToUpper(strings.TrimSpace(input.Method))
		identity = []any{input.Type, input.URL, method}
		if method != "" {
			if len(method) > 20 || strings.IndexFunc(method, func(r rune) bool { return !(r >= 'A' && r <= 'Z' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) }) >= 0 {
				return p, fmt.Errorf("HTTP 方法无效")
			}
			p.Method = &method
		}
		if input.TargetID > 0 {
			p.Projection = "endpoint"
		}
	default:
		return p, fmt.Errorf("不支持的资产类型")
	}
	if len(p.Label) > 8192 {
		return p, ErrInvalidAsset
	}
	p.Identity, _ = json.Marshal(identity)
	return p, nil
}
