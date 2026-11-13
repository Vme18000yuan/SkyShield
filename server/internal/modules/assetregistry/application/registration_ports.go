package application

import (
	"context"
	"encoding/json"
	"errors"

	assetdomain "github.com/yyhuni/lunafox/server/internal/modules/asset/domain"
)

var (
	ErrInvalidAsset         = errors.New("invalid asset")
	ErrAssetWriteForbidden  = errors.New("asset write permission required")
	ErrAssetTargetNotFound  = errors.New("active target not found")
	ErrAssetOutsideTarget   = errors.New("asset is outside the selected target")
	ErrAssetRequestConflict = errors.New("asset request ID was reused with different input")
)

type PortObservation struct {
	Port     int    `json:"port"`
	Service  string `json:"service,omitempty"`
	Protocol string `json:"protocol,omitempty"`
}

// AssetInput retains ARTEX asset information while separating credentials from
// ordinary observation JSON before persistence.
type AssetInput struct {
	Type           string            `json:"type"`
	TargetID       int               `json:"targetId,omitempty"`
	OrganizationID int               `json:"organizationId,omitempty"`
	Domain         string            `json:"domain,omitempty"`
	ICP            string            `json:"icp,omitempty"`
	RecordType     string            `json:"recordType,omitempty"`
	RecordValue    []string          `json:"recordValue,omitempty"`
	IP             string            `json:"ip,omitempty"`
	BoundDomains   []string          `json:"boundDomains,omitempty"`
	OpenPorts      []PortObservation `json:"openPorts,omitempty"`
	AppName        string            `json:"appName,omitempty"`
	BundleID       string            `json:"bundleId,omitempty"`
	Platform       string            `json:"platform,omitempty"`
	Category       string            `json:"category,omitempty"`
	Description    string            `json:"description,omitempty"`
	AppICP         string            `json:"appIcp,omitempty"`
	URL            string            `json:"url,omitempty"`
	Technologies   []string          `json:"technologies,omitempty"`
	StatusCode     *int64            `json:"statusCode,omitempty"`
	ContentLength  *int64            `json:"contentLength,omitempty"`
	PageTitle      string            `json:"pageTitle,omitempty"`
	Webserver      string            `json:"webserver,omitempty"`
	FaviconMMH3    string            `json:"faviconMmh3,omitempty"`
	Auth           []map[string]any  `json:"auth,omitempty"`
	ServiceName    string            `json:"serviceName,omitempty"`
	ServiceIP      string            `json:"serviceIp,omitempty"`
	Port           int               `json:"port,omitempty"`
	Proto          string            `json:"proto,omitempty"`
	Method         string            `json:"method,omitempty"`
	Params         []map[string]any  `json:"params,omitempty"`
	Extra          map[string]any    `json:"extra,omitempty"`
}

type PreparedAsset struct {
	Input      AssetInput
	Label      string
	Identity   json.RawMessage
	Method     *string
	RecordType *string
	Projection string
}

type RegistrationCommand struct {
	UserID        int
	Asset         PreparedAsset
	Origin        string
	SourceKey     string
	Fingerprint   string
	Payload       json.RawMessage
	EncryptedAuth []byte
	Task          *RegistrationTaskScope
}

// RegistrationTaskScope is server-owned attribution for an active AI run.
// It is never accepted from the public asset registration API.
type RegistrationTaskScope struct {
	TaskID        int64
	ExplorationID int64
	IntentID      int64
	ExecutionID   string
}

type RegisteredAsset struct {
	ID         int64
	Type       string
	Projection string
}

type RegistrationStore interface {
	RegisterAsset(context.Context, RegistrationCommand) (RegisteredAsset, error)
}

type LegacyAssetCreator interface {
	BatchCreateContext(context.Context, int, []string) (int, error)
}

type LegacyWebObservationMerger interface {
	MergeWebObservationContext(context.Context, int, string, assetdomain.SparseWebObservation) error
}

type LegacyAssetCreators struct {
	Websites   LegacyAssetCreator
	Endpoints  LegacyAssetCreator
	Subdomains LegacyAssetCreator
}

type AssetSecretCodec interface {
	Seal(string, string) ([]byte, error)
	Fingerprint(string, []byte) (string, error)
}

type BatchRegisterInput struct {
	UserID    int
	Origin    string
	RequestID string
	Assets    []AssetInput
	Task      *RegistrationTaskScope
}

type AssetRegistrationResult struct {
	Index      int    `json:"index"`
	ID         string `json:"id"`
	Type       string `json:"type"`
	Projection string `json:"projection"`
}

type AssetRegistrationError struct {
	Index int    `json:"index"`
	Error string `json:"error"`
}

type BatchRegisterResult struct {
	RequestID string                    `json:"requestId"`
	Results   []AssetRegistrationResult `json:"results"`
	Errors    []AssetRegistrationError  `json:"errors"`
}
