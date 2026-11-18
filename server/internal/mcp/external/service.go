// Package external serves named MCP clients with explicit tool and organization grants.
package external

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yyhuni/lunafox/server/internal/aivuln"
	"github.com/yyhuni/lunafox/server/internal/mcp/admission"
	"github.com/yyhuni/lunafox/server/internal/mcp/audit"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	identity "github.com/yyhuni/lunafox/server/internal/modules/identity/infrastructure"
	security "github.com/yyhuni/lunafox/server/internal/modules/security/application"
	"github.com/yyhuni/lunafox/server/internal/pkg/dbtx"
	"gorm.io/gorm"
)

type Client struct {
	IsDefault        bool           `json:"isDefault"`
	ID               int            `json:"id"`
	OwnerUserID      int            `json:"-"`
	Name             string         `json:"name"`
	DefaultKeyID     *int           `json:"-"`
	Enabled          bool           `json:"enabled"`
	Legacy           bool           `json:"legacy"`
	AllOrganizations bool           `json:"allOrganizations"`
	OrganizationIDs  pq.Int64Array  `gorm:"type:integer[]" json:"organizationIds"`
	Tools            pq.StringArray `gorm:"type:text[]" json:"tools"`
	Role             string         `json:"-"`
}

type Service struct {
	db              *gorm.DB
	assets          *assets.Facade
	evidence        *aivuln.Service
	vulnerabilities *security.VulnerabilityFacade
	stream          http.Handler
	limiter         *admission.Limiter
	audit           audit.Emitter
}

type clientContext struct{}

func (s *Service) query(ctx context.Context) *gorm.DB {
	return dbtx.Resolve(ctx, s.db).WithContext(ctx)
}

var errDenied = errors.New("resource unavailable in this client's scope")
var errInput = errors.New("invalid arguments")

func New(db *gorm.DB, inventory *assets.Facade, evidence *aivuln.Service, vulnerabilities *security.VulnerabilityFacade) *Service {
	s := &Service{db: db, assets: inventory, evidence: evidence, vulnerabilities: vulnerabilities, limiter: admission.NewLimiter(60, 10, 4), audit: audit.NewLogger()}
	s.stream = mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		client := r.Context().Value(clientContext{}).(Client)
		server := mcp.NewServer(&mcp.Implementation{Name: "skyshield", Title: "天穹 SkyShield", Version: "1.0.0"}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}})
		s.registerTools(server, client)
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 256 << 10, PropagateRequestCancellation: true})
	return s
}

const activeMember = ` JOIN tenant_membership m ON m.user_id=c.owner_user_id AND m.tenant_id=?
 JOIN auth_user u ON u.id=m.user_id AND u.is_active JOIN tenant tn ON tn.id=m.tenant_id AND tn.status='active' `

func (s *Service) authenticate(ctx context.Context, secret string) (Client, error) {
	var client Client
	digest := identity.NewMCPKeySecretGenerator().Digest(secret)
	result := s.db.WithContext(ctx).Raw(`SELECT c.id,c.owner_user_id,c.name,c.default_key_id,c.enabled,c.legacy,c.all_organizations,c.organization_ids,c.tools,m.role
      FROM mcp_client c LEFT JOIN mcp_key k ON k.id=c.default_key_id AND k.user_id=c.owner_user_id `+activeMember+
		` WHERE c.enabled AND (c.key_digest=? OR k.key_digest=?)`, assets.DefaultTenantID, digest, digest).Scan(&client)
	if result.Error != nil {
		return client, result.Error
	}
	if result.RowsAffected != 1 {
		return client, errDenied
	}
	return client, nil
}

// The historical handler remains available only to credentials issued before
// the migration on its original ingress. A scoped key never falls back to it.
func (s *Service) Handler(legacy http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 512 {
			unauthorized(w)
			return
		}
		client, err := s.authenticate(r.Context(), parts[1])
		if errors.Is(err, errDenied) {
			unauthorized(w)
			return
		}
		if err != nil {
			http.Error(w, "MCP authentication unavailable", http.StatusServiceUnavailable)
			return
		}
		if client.Legacy {
			if legacy == nil {
				http.Error(w, "Save client permissions to enable this endpoint", http.StatusForbidden)
				return
			}
			legacy.ServeHTTP(w, r)
			return
		}
		if !browserOriginAllowed(r) {
			http.Error(w, "MCP origin is not allowed", http.StatusForbidden)
			return
		}
		if ok, delay := s.limiter.Allow(client.ID); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(admission.RetryAfterSeconds(delay)))
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		release, ok := s.limiter.Acquire(client.ID)
		if !ok {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		defer release()
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
		if err != nil {
			http.Error(w, "Invalid MCP request body", http.StatusRequestEntityTooLarge)
			return
		}
		var msg struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(body, &msg) != nil || msg.Method == "" {
			http.Error(w, "Expected one JSON-RPC request", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		r.Body = io.NopCloser(bytes.NewReader(body))
		s.stream.ServeHTTP(w, r.WithContext(context.WithValue(ctx, clientContext{}, client)))
	})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="skyshield-mcp"`)
	w.WriteHeader(http.StatusUnauthorized)
}

// Desktop clients usually omit Origin. A browser diagnostic must originate
// from the UI itself. The local frontend and Nginx preserve the public host.
func browserOriginAllowed(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	value := r.Header.Get("Origin")
	if value == "" {
		return true
	}
	origin, err := url.Parse(value)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	if strings.EqualFold(origin.Host, r.Host) {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	peer := net.ParseIP(host)
	return err == nil && peer != nil && (peer.IsLoopback() || peer.IsPrivate()) &&
		strings.EqualFold(origin.Host, r.Header.Get("X-Forwarded-Host")) && origin.Scheme == r.Header.Get("X-Forwarded-Proto")
}

func (c Client) permitsOrganization(id int) bool {
	return id > 0 && id <= 2147483647 && (c.AllOrganizations || slices.Contains(c.OrganizationIDs, int64(id)))
}
func (c Client) permitsTool(name string) bool {
	return slices.Contains(c.Tools, name) && (name != toolReview || c.Role == "admin" || c.Role == "analyst")
}
