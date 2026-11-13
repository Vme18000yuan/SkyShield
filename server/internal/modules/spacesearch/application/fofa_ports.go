package application

import (
	"context"
	"errors"
	"time"
)

var (
	ErrForbidden      = errors.New("network search permission required")
	ErrInvalidInput   = errors.New("invalid network search input")
	ErrNotConfigured  = errors.New("FOFA credentials are not configured")
	ErrSearchNotFound = errors.New("FOFA search snapshot not found or expired")
	ErrDestination    = errors.New("active destination organization or target not found")
	ErrProvider       = errors.New("FOFA request failed")
)

type Access struct{ CanSearch, CanManage bool }
type Settings struct {
	Email        string
	EncryptedKey []byte
	UpdatedAt    time.Time
}
type SettingsView struct {
	Email      string     `json:"email"`
	Configured bool       `json:"configured"`
	CanManage  bool       `json:"canManage"`
	CanSearch  bool       `json:"canSearch"`
	UpdatedAt  *time.Time `json:"updatedAt"`
}
type Credentials struct{ Email, APIKey string }
type SearchInput struct {
	Query    string `json:"query"`
	Page     int    `json:"page"`
	PageSize int    `json:"pageSize"`
	Full     bool   `json:"full"`
}
type Record struct {
	RowID        int    `json:"rowId"`
	Host         string `json:"host"`
	IP           string `json:"ip"`
	Port         int    `json:"port"`
	Protocol     string `json:"protocol"`
	Domain       string `json:"domain"`
	Title        string `json:"title"`
	Server       string `json:"server"`
	Country      string `json:"country"`
	CountryName  string `json:"countryName"`
	Region       string `json:"region"`
	City         string `json:"city"`
	ICP          string `json:"icp"`
	BaseProtocol string `json:"baseProtocol"`
	URL          string `json:"url"`
	Hostname     string `json:"hostname"`
	Target       string `json:"target"`
	Importable   bool   `json:"importable"`
	ImportError  string `json:"importError"`
}
type SearchResult struct {
	Total   int64
	Results []Record
}
type SearchPage struct {
	ID        string    `json:"id"`
	Query     string    `json:"query"`
	Page      int       `json:"page"`
	PageSize  int       `json:"pageSize"`
	Full      bool      `json:"full"`
	Total     int64     `json:"total"`
	Results   []Record  `json:"results"`
	HasNext   bool      `json:"hasNext"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type ImportInput struct {
	UserID         int
	SearchID       string
	Rows           []int
	OrganizationID int
	TargetID       int
}
type ImportSelection struct {
	UserID         int
	SearchID       string
	RowID          int
	OrganizationID int
	TargetID       int
}
type ImportCommand struct {
	Selection ImportSelection
	Record    Record
	Query     string
}
type ImportRowResult struct {
	RowID    int      `json:"rowId"`
	TargetID int      `json:"targetId"`
	AssetIDs []string `json:"assetIds"`
	Warnings []string `json:"warnings"`
	Reused   bool     `json:"reused"`
}
type ImportRowError struct {
	RowID   int    `json:"rowId"`
	Message string `json:"message"`
}
type ImportResult struct {
	ImportedRows int               `json:"importedRows"`
	FailedRows   int               `json:"failedRows"`
	AssetCount   int               `json:"assetCount"`
	Results      []ImportRowResult `json:"results"`
	Errors       []ImportRowError  `json:"errors"`
}
type Store interface {
	Access(context.Context, int) (Access, error)
	GetSettings(context.Context, int) (*Settings, error)
	SaveSettings(context.Context, int, Settings, bool) error
	SaveSearch(context.Context, int, SearchPage) error
	ImportRecord(context.Context, ImportSelection, func(context.Context, Record, string) (ImportRowResult, error)) (ImportRowResult, error)
}
type Provider interface {
	Search(context.Context, Credentials, SearchInput) (SearchResult, error)
	Test(context.Context, Credentials) error
}
type SecretCodec interface {
	Seal(string, string) ([]byte, error)
	Open(string, []byte) (string, error)
}
type AssetImporter interface {
	Import(context.Context, ImportCommand) (ImportRowResult, error)
}
