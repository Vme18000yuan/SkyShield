package handler

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yyhuni/lunafox/server/internal/middleware"
	assetapp "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"github.com/yyhuni/lunafox/server/internal/modules/httpdto"
)

type assetReader interface {
	OrganizationOverview(context.Context, int, int, bool) (*assetapp.OrganizationAssetOverview, error)
	List(context.Context, assetapp.ListInput) (*assetapp.ListResult, error)
	ListPorts(context.Context, assetapp.PortListInput) (*assetapp.PortListResult, error)
	ListInventory(context.Context, assetapp.PortListInput) (*assetapp.InventoryResult, error)
	ApplicationOverview(context.Context, assetapp.ApplicationOverviewInput) (*assetapp.ApplicationOverview, error)
}

func (h *AssetEntityHandler) ApplicationOverview(c *gin.Context) {
	user, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpdto.BadRequest(c, "Invalid application")
		return
	}
	org, ok := parseOptionalInt(c, "organizationId")
	if !ok {
		httpdto.BadRequest(c, "Invalid organizationId")
		return
	}
	page, ok := parseOptionalInt(c, "page")
	if !ok {
		httpdto.BadRequest(c, "Invalid page")
		return
	}
	size, ok := parseOptionalInt(c, "pageSize")
	if !ok {
		httpdto.BadRequest(c, "Invalid pageSize")
		return
	}
	result, err := h.reader.ApplicationOverview(c.Request.Context(), assetapp.ApplicationOverviewInput{UserID: user, ApplicationID: id, OrganizationID: org, Page: page, PageSize: size})
	if err != nil {
		switch {
		case errors.Is(err, assetapp.ErrInvalidQuery):
			httpdto.BadRequest(c, "Invalid application query")
		case errors.Is(err, assetapp.ErrForbidden):
			httpdto.Forbidden(c, "Tenant membership required")
		case errors.Is(err, assetapp.ErrApplicationNotFound):
			httpdto.NotFound(c, "Application not found")
		default:
			httpdto.InternalError(c, "Failed to load application")
		}
		return
	}
	httpdto.Success(c, gin.H{"application": toAssetEntityResponse(result.Asset), "websites": result.Websites, "totalSize": result.TotalSize, "page": result.Page})
}

func toAssetEntityResponse(item assetapp.AssetEntity) assetEntityResponse {
	return assetEntityResponse{Name: httpdto.AssetEntityName(item.ID), ID: strconv.FormatInt(item.ID, 10), AssetType: item.AssetType, DisplayType: item.DisplayType, Label: item.Label, Method: item.Method, FirstSeenAt: item.FirstSeenAt.UTC(), LastSeenAt: item.LastSeenAt.UTC(), SourceCount: item.SourceCount, Derived: item.Derived, ApplicationSources: item.ApplicationSources, Organizations: item.Organizations}
}

func (h *AssetEntityHandler) ListInventory(c *gin.Context) {
	user, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
		return
	}
	page, ok := parseOptionalInt(c, "page")
	if !ok {
		httpdto.BadRequest(c, "Invalid page")
		return
	}
	size, ok := parseOptionalInt(c, "pageSize")
	if !ok {
		httpdto.BadRequest(c, "Invalid pageSize")
		return
	}
	org, ok := parseOptionalInt(c, "organizationId")
	if !ok {
		httpdto.BadRequest(c, "Invalid organizationId")
		return
	}
	root, ok := parseOptionalID(c, "rootDomainId")
	if !ok {
		httpdto.BadRequest(c, "Invalid rootDomainId")
		return
	}
	hideCDN, ok := httpdto.OptionalQueryBool(c, "hideCdn")
	if !ok {
		return
	}
	result, err := h.reader.ListInventory(c.Request.Context(), assetapp.PortListInput{HideCDN: hideCDN, UserID: user, Page: page, PageSize: size, OrganizationID: org, RootDomainID: root, Query: c.Query("q")})
	if err != nil {
		switch {
		case errors.Is(err, assetapp.ErrInvalidQuery):
			httpdto.BadRequest(c, "Invalid inventory query")
		case errors.Is(err, assetapp.ErrForbidden):
			httpdto.Forbidden(c, "Tenant membership required")
		default:
			httpdto.InternalError(c, "Failed to list inventory")
		}
		return
	}
	items := make([]gin.H, 0, len(result.Items))
	for _, item := range result.Items {
		if item.Asset != nil {
			items = append(items, gin.H{"kind": "asset", "asset": toAssetEntityResponse(*item.Asset)})
		} else if item.Port != nil {
			items = append(items, gin.H{"kind": "port", "port": item.Port})
		}
	}
	httpdto.Success(c, gin.H{"items": items, "totalSize": result.TotalSize, "page": result.Page})
}

func parseOptionalInt(c *gin.Context, name string) (int, bool) {
	raw := c.Query(name)
	if raw == "" {
		return 0, true
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil && value > 0
}
func parseOptionalID(c *gin.Context, name string) (int64, bool) {
	raw := c.Query(name)
	if raw == "" {
		return 0, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	return value, err == nil && value > 0
}

// ListPorts presents distinct host:port observations with service evidence.
func (h *AssetEntityHandler) ListPorts(c *gin.Context) {
	user, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
		return
	}
	page, ok := parseOptionalInt(c, "page")
	if !ok {
		httpdto.BadRequest(c, "Invalid page")
		return
	}
	size, ok := parseOptionalInt(c, "pageSize")
	if !ok {
		httpdto.BadRequest(c, "Invalid pageSize")
		return
	}
	org, ok := parseOptionalInt(c, "organizationId")
	if !ok {
		httpdto.BadRequest(c, "Invalid organizationId")
		return
	}
	root, ok := parseOptionalID(c, "rootDomainId")
	if !ok {
		httpdto.BadRequest(c, "Invalid rootDomainId")
		return
	}
	hideCDN, ok := httpdto.OptionalQueryBool(c, "hideCdn")
	if !ok {
		return
	}
	result, err := h.reader.ListPorts(c.Request.Context(), assetapp.PortListInput{HideCDN: hideCDN, UserID: user, Page: page, PageSize: size, OrganizationID: org, RootDomainID: root, Query: c.Query("q")})
	if err != nil {
		switch {
		case errors.Is(err, assetapp.ErrInvalidQuery):
			httpdto.BadRequest(c, "Invalid port query")
		case errors.Is(err, assetapp.ErrForbidden):
			httpdto.Forbidden(c, "Tenant membership required")
		default:
			httpdto.InternalError(c, "Failed to list ports")
		}
		return
	}
	httpdto.Success(c, result)
}

// RootDomainOverview reports a visible root and scoped related-asset totals.
func (h *AssetEntityHandler) RootDomainOverview(c *gin.Context) {
	user, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpdto.BadRequest(c, "Invalid root domain")
		return
	}
	org, ok := parseOptionalInt(c, "organizationId")
	if !ok {
		httpdto.BadRequest(c, "Invalid organizationId")
		return
	}
	hideCDN, ok := httpdto.OptionalQueryBool(c, "hideCdn")
	if !ok {
		return
	}
	root, err := h.reader.List(c.Request.Context(), assetapp.ListInput{HideCDN: hideCDN, UserID: user, AssetType: "root_domain", AssetID: id, OrganizationID: org, PageSize: 1})
	if err != nil {
		if errors.Is(err, assetapp.ErrInvalidQuery) {
			httpdto.BadRequest(c, "Invalid root domain query")
		} else if errors.Is(err, assetapp.ErrForbidden) {
			httpdto.Forbidden(c, "Tenant membership required")
		} else {
			httpdto.InternalError(c, "Failed to load root domain")
		}
		return
	}
	if len(root.Items) == 0 {
		httpdto.NotFound(c, "Root domain not found")
		return
	}
	counts := map[string]int64{}
	for _, item := range []struct{ key, kind, category string }{{"subdomains", "subdomain", ""}, {"ips", "ip", ""}, {"apps", "app", ""}, {"web", "", "web"}, {"urls", "endpoint", ""}} {
		result, e := h.reader.List(c.Request.Context(), assetapp.ListInput{HideCDN: hideCDN, UserID: user, AssetType: item.kind, AssetCategory: item.category, RootDomainID: id, OrganizationID: org, Page: 1, PageSize: 1})
		if e != nil {
			httpdto.InternalError(c, "Failed to load root domain assets")
			return
		}
		counts[item.key] = result.TotalSize
	}
	ports, err := h.reader.ListPorts(c.Request.Context(), assetapp.PortListInput{HideCDN: hideCDN, UserID: user, RootDomainID: id, OrganizationID: org, Page: 1, PageSize: 1})
	if err != nil {
		httpdto.InternalError(c, "Failed to load root domain ports")
		return
	}
	counts["ports"] = ports.TotalSize
	item := root.Items[0]
	httpdto.Success(c, gin.H{"id": strconv.FormatInt(item.ID, 10), "domain": item.Label, "organizations": item.Organizations, "firstSeenAt": item.FirstSeenAt.UTC(), "lastSeenAt": item.LastSeenAt.UTC(), "counts": counts})
}

type AssetEntityHandler struct{ reader assetReader }

func NewAssetEntityHandler(reader assetReader) *AssetEntityHandler {
	return &AssetEntityHandler{reader: reader}
}

type assetEntityResponse struct {
	Name               string                       `json:"name"`
	ID                 string                       `json:"id"`
	AssetType          string                       `json:"assetType"`
	DisplayType        string                       `json:"displayType"`
	Label              string                       `json:"label"`
	Method             *string                      `json:"method"`
	FirstSeenAt        time.Time                    `json:"firstSeenAt"`
	LastSeenAt         time.Time                    `json:"lastSeenAt"`
	SourceCount        int64                        `json:"sourceCount"`
	Derived            bool                         `json:"derived"`
	ApplicationSources int64                        `json:"applicationSources"`
	Organizations      []assetapp.AssetOrganization `json:"organizations"`
}

// List returns canonical identities belonging to the authenticated user's tenant.
// GET /v1/assetEntities
func (h *AssetEntityHandler) List(c *gin.Context) {
	userID, ok := middleware.GetUserID(c)
	if !ok {
		httpdto.Unauthorized(c, "Not authenticated")
		return
	}
	pageSize := 0
	if raw := c.Query("pageSize"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			httpdto.BadRequest(c, "Invalid pageSize")
			return
		}
		pageSize = parsed
	}
	page, organizationID := 0, 0
	for key, destination := range map[string]*int{"page": &page, "organizationId": &organizationID} {
		if raw := c.Query(key); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed <= 0 {
				httpdto.BadRequest(c, "Invalid "+key)
				return
			}
			*destination = parsed
		}
	}
	var rootID int64
	if raw := c.Query("rootDomainId"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			httpdto.BadRequest(c, "Invalid rootDomainId")
			return
		}
		rootID = parsed
	}
	hideCDN, ok := httpdto.OptionalQueryBool(c, "hideCdn")
	if !ok {
		return
	}
	result, err := h.reader.List(c.Request.Context(), assetapp.ListInput{
		HideCDN: hideCDN, UserID: userID, AssetType: c.Query("assetType"), AssetCategory: c.Query("assetCategory"), Query: c.Query("q"),
		PageSize: pageSize, PageToken: c.Query("pageToken"), Page: page, OrganizationID: organizationID, RootDomainID: rootID,
	})
	if err != nil {
		switch {
		case errors.Is(err, assetapp.ErrInvalidQuery):
			httpdto.BadRequest(c, "Invalid asset query")
		case errors.Is(err, assetapp.ErrForbidden):
			httpdto.Forbidden(c, "Tenant membership required")
		default:
			httpdto.InternalError(c, "Failed to list assets")
		}
		return
	}
	items := make([]assetEntityResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, toAssetEntityResponse(item))
	}
	response := gin.H{"assetEntities": items, "nextPageToken": result.NextPageToken}
	if page > 0 {
		response["totalSize"] = result.TotalSize
		response["page"] = page
	}
	httpdto.Success(c, response)
}
