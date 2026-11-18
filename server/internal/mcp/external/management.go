package external

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	httpdto "github.com/yyhuni/lunafox/server/internal/dto"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	identity "github.com/yyhuni/lunafox/server/internal/modules/identity/infrastructure"
	"gorm.io/gorm"
)

func (s *Service) RegisterManagement(g *gin.RouterGroup) {
	group := g.Group("/mcpClients")
	group.Use(func(c *gin.Context) {
		user, ok := middleware.GetUserID(c)
		if !ok {
			httpdto.Unauthorized(c, "Sign in required")
			c.Abort()
			return
		}
		var role string
		result := s.db.WithContext(c.Request.Context()).Raw(`SELECT m.role FROM tenant_membership m
          JOIN auth_user u ON u.id=m.user_id AND u.is_active JOIN tenant tn ON tn.id=m.tenant_id AND tn.status='active'
          WHERE m.tenant_id=? AND m.user_id=?`, assets.DefaultTenantID, user).Scan(&role)
		if result.Error != nil {
			httpdto.InternalError(c, "MCP client storage unavailable")
			c.Abort()
			return
		}
		if role == "" {
			httpdto.Forbidden(c, "Active membership required")
			c.Abort()
			return
		}
		c.Set("mcp_owner", user)
		c.Set("mcp_role", role)
		c.Next()
	})
	group.GET("", s.listClients)
	group.POST("", s.saveClient)
	group.PUT("/:id", s.saveClient)
	group.POST("/:id/rotate", s.rotateClient)
	group.DELETE("/:id", s.deleteClient)
}

const clientColumns = "id, name, default_key_id IS NOT NULL AS is_default, enabled, legacy, all_organizations, organization_ids, tools"

func (s *Service) listClients(c *gin.Context) {
	clients := []Client{}
	err := s.db.WithContext(c.Request.Context()).Table("mcp_client").Select(clientColumns).
		Where("owner_user_id=?", c.GetInt("mcp_owner")).Order("id").Find(&clients).Error
	if err != nil {
		managementError(c, err)
		return
	}
	allowed := []string{}
	for _, name := range catalogNames {
		if name != toolReview || mayReview(c.GetString("mcp_role")) {
			allowed = append(allowed, name)
		}
	}
	httpdto.Success(c, gin.H{"clients": clients, "availableTools": allowed})
}

type policyInput struct {
	Name             string   `json:"name"`
	AllOrganizations bool     `json:"allOrganizations"`
	OrganizationIDs  []int64  `json:"organizationIds"`
	Tools            []string `json:"tools"`
	Enabled          *bool    `json:"enabled"`
}

func mayReview(role string) bool { return role == "admin" || role == "analyst" }

func (p policyInput) validate(ctx context.Context, tx *gorm.DB, role string) error {
	if p.Name == "" || utf8.RuneCountInString(p.Name) > 100 || strings.ContainsAny(p.Name, "\x00\r\n") || p.Enabled == nil ||
		len(p.OrganizationIDs) > 2000 || (p.AllOrganizations && len(p.OrganizationIDs) > 0) || len(p.Tools) > len(catalogNames) {
		return errInput
	}
	tools := map[string]bool{}
	for _, name := range p.Tools {
		if toolDescriptions[name] == "" || tools[name] {
			return errInput
		}
		if name == toolReview && !mayReview(role) {
			return errDenied
		}
		tools[name] = true
	}
	if len(p.OrganizationIDs) > 0 {
		var count int
		err := tx.WithContext(ctx).Raw("SELECT count(*) FROM organization WHERE deleted_at IS NULL AND id=ANY(?::bigint[])", pq.Array(p.OrganizationIDs)).Scan(&count).Error
		if err != nil {
			return err
		}
		if count != len(p.OrganizationIDs) {
			return errInput
		}
	}
	return nil
}

func routeID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 || id > 2147483647 {
		httpdto.BadRequest(c, "Invalid client ID")
		return 0, false
	}
	return id, true
}

func (s *Service) saveClient(c *gin.Context) {
	create := c.Request.Method == http.MethodPost
	owner := c.GetInt("mcp_owner")
	id := 0
	if !create {
		var ok bool
		id, ok = routeID(c)
		if !ok {
			return
		}
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 96<<10)
	var p policyInput
	if !httpdto.BindJSON(c, &p) {
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.OrganizationIDs == nil {
		p.OrganizationIDs = []int64{}
	}
	if p.Tools == nil {
		p.Tools = []string{}
	}
	secret := ""
	ctx := c.Request.Context()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := p.validate(ctx, tx, c.GetString("mcp_role")); err != nil {
			return err
		}
		if create {
			key, digest, err := identity.NewMCPKeySecretGenerator().Generate()
			if err != nil {
				return err
			}
			secret = key
			return tx.Raw(`INSERT INTO mcp_client(owner_user_id,name,key_digest,enabled,all_organizations,organization_ids,tools)
              VALUES (?,?,?,?,?,?::integer[],?::text[]) RETURNING id`, owner, p.Name, digest, *p.Enabled, p.AllOrganizations, pq.Array(p.OrganizationIDs), pq.Array(p.Tools)).Row().Scan(&id)
		}
		r := tx.Exec(`UPDATE mcp_client SET name=?,enabled=?,legacy=false,all_organizations=?,organization_ids=?::integer[],tools=?::text[]
          WHERE id=? AND owner_user_id=?`, p.Name, *p.Enabled, p.AllOrganizations, pq.Array(p.OrganizationIDs), pq.Array(p.Tools), id, owner)
		return affected(r)
	})
	if err != nil {
		managementError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	if create {
		httpdto.Created(c, gin.H{"id": id, "key": secret})
		return
	}
	httpdto.Success(c, gin.H{"id": id})
}

func affected(r *gorm.DB) error {
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *Service) rotateClient(c *gin.Context) { s.changeCredential(c, true) }
func (s *Service) deleteClient(c *gin.Context) { s.changeCredential(c, false) }

func (s *Service) changeCredential(c *gin.Context, rotate bool) {
	id, ok := routeID(c)
	if !ok {
		return
	}
	owner := c.GetInt("mcp_owner")
	var key string
	err := s.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var row Client
		r := tx.Raw("SELECT id,default_key_id FROM mcp_client WHERE id=? AND owner_user_id=? FOR UPDATE", id, owner).Scan(&row)
		if err := affected(r); err != nil {
			return err
		}
		table := "mcp_client"
		target := id
		if row.DefaultKeyID != nil {
			table = "mcp_key"
			target = *row.DefaultKeyID
		}
		if !rotate {
			return affected(tx.Exec("DELETE FROM "+table+" WHERE id=?", target))
		}
		plain, digest, err := identity.NewMCPKeySecretGenerator().Generate()
		if err != nil {
			return err
		}
		key = plain
		if row.DefaultKeyID != nil {
			return affected(tx.Exec("UPDATE mcp_key SET key_digest=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND user_id=?", digest, target, owner))
		}
		return affected(tx.Exec("UPDATE mcp_client SET key_digest=? WHERE id=?", digest, target))
	})
	if err != nil {
		managementError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	if rotate {
		httpdto.Success(c, gin.H{"id": id, "key": key})
		return
	}
	c.Status(http.StatusNoContent)
}

func managementError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errInput):
		httpdto.BadRequest(c, "Invalid client name, tools or organizations")
	case errors.Is(err, errDenied):
		httpdto.Forbidden(c, "Permission denied")
	case errors.Is(err, gorm.ErrRecordNotFound):
		httpdto.NotFound(c, "Client not found")
	default:
		httpdto.InternalError(c, "MCP client storage unavailable")
	}
}
