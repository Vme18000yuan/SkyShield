package external

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yyhuni/lunafox/server/internal/mcp/audit"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	"github.com/yyhuni/lunafox/server/internal/pkg/dbtx"
	"gorm.io/gorm"
)

const toolReview = "set_vulnerability_review"

var toolDescriptions = map[string]string{
	"list_organizations":              "List organizations available to this client.",
	"get_organization_asset_overview": "Count root domains, subdomains and IPs in one organization. CDN nodes are hidden by default.",
	"search_asset_inventory":          "Search stored assets and ports in one organization. CDN nodes are hidden by default.",
	"list_vulnerabilities":            "List registered vulnerabilities in one organization.",
	"get_vulnerability":               "Read a vulnerability's name, type, source, severity and review status in one organization.",
	"get_vulnerability_http_evidence": "Read redacted, recorded HTTP request and response evidence in one organization.",
	"list_ai_tasks":                   "List existing AI tasks owned by the credential owner within the authorized organization scope.",
	"get_ai_task_result":              "Read a saved task outcome, findings and available report IDs.",
	"get_ai_finding_report":           "Read the saved Markdown report for an authorized task finding.",
	"list_ai_conversations":           "List visible conversations of the credential owner. Organization-limited clients see only conversations with an in-scope finding.",
	"get_ai_conversation_result":      "Read saved conversation results. Organization-limited clients receive only in-scope findings.",
	toolReview:                        "Set the review status of one authorized vulnerability.",
}
var catalogNames = slices.Sorted(maps.Keys(toolDescriptions))

func (s *Service) registerTools(server *mcp.Server, c Client) {
	addTool(s, server, c, "list_organizations", s.organizations)
	addTool(s, server, c, "get_organization_asset_overview", s.overview)
	addTool(s, server, c, "search_asset_inventory", s.inventory)
	addTool(s, server, c, "list_vulnerabilities", s.listVulnerabilities)
	addTool(s, server, c, "get_vulnerability", s.getVulnerability)
	addTool(s, server, c, "get_vulnerability_http_evidence", s.httpEvidence)
	addTool(s, server, c, "list_ai_tasks", s.tasks)
	addTool(s, server, c, "get_ai_task_result", s.taskResult)
	addTool(s, server, c, "get_ai_finding_report", s.findingReport)
	addTool(s, server, c, "list_ai_conversations", s.conversations)
	addTool(s, server, c, "get_ai_conversation_result", s.conversationResult)
	addTool(s, server, c, toolReview, s.review)
}

func addTool[I any](s *Service, server *mcp.Server, c Client, name string, read func(context.Context, Client, I) (any, error)) {
	if !c.permitsTool(name) {
		return
	}
	mcp.AddTool(server, &mcp.Tool{Name: name, Description: toolDescriptions[name],
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: name != toolReview}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in I) (*mcp.CallToolResult, any, error) {
			start := time.Now()
			var result any
			var err error
			switch name {
			case "get_vulnerability_http_evidence", "get_ai_task_result", "get_ai_finding_report", "get_ai_conversation_result":
				// Membership and the detail rows it protects must come from one snapshot.
				err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
					var readErr error
					result, readErr = read(dbtx.WithTransaction(ctx, tx), c, in)
					return readErr
				}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
			default:
				result, err = read(ctx, c, in)
			}
			category := "SUCCESS"
			if err == nil {
				body, e := json.Marshal(result)
				if e != nil || len(body) > 448<<10 {
					err = errors.New("result too large; request a smaller page")
					category = "RESULT_TOO_LARGE"
				}
			} else {
				switch {
				case errors.Is(err, errInput), errors.Is(err, assets.ErrInvalidQuery):
					err = errInput
					category = "INVALID_ARGUMENT"
				case errors.Is(err, errDenied), errors.Is(err, assets.ErrOrganizationNotFound), errors.Is(err, assets.ErrForbidden):
					err = errDenied
					category = "NOT_FOUND"
				case errors.Is(err, context.DeadlineExceeded):
					category = "TIMEOUT"
				default:
					err = errors.New("could not read or update the requested record")
					category = "INTERNAL"
				}
			}
			s.audit.Emit(audit.Event{Method: "tools/call", Tool: name, UserID: c.OwnerUserID, KeyRecordID: c.ID, ResultCategory: category, Duration: time.Since(start)})
			if err != nil {
				return nil, nil, err
			}
			return nil, result, nil
		})
}

type Page struct {
	Page     int `json:"page,omitempty" jsonschema:"Page number starting at one."`
	PageSize int `json:"page_size,omitempty" jsonschema:"Records per page, from 1 to 100."`
}

func (p Page) limits() (int, int, error) {
	page, size := p.Page, p.PageSize
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 20
	}
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		return 0, 0, errInput
	}
	return size, (page - 1) * size, nil
}
