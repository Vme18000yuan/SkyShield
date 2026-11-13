package application

import (
	"context"
	"errors"
	"time"
)

var ErrApplicationNotFound = errors.New("application not found")

type ApplicationWebsite struct {
	ID            int64     `json:"id"`
	URL           string    `json:"url"`
	Host          string    `json:"host"`
	Title         string    `json:"title"`
	StatusCode    *int64    `json:"statusCode"`
	Technologies  []string  `json:"technologies"`
	EvidenceKind  string    `json:"evidenceKind"`
	EvidenceValue string    `json:"evidenceValue"`
	FirstSeenAt   time.Time `json:"firstSeenAt"`
}
type ApplicationWebsiteQuery struct {
	TenantID       string
	ApplicationID  int64
	OrganizationID int
	Offset         int
	Limit          int
}
type ApplicationOverviewInput struct {
	UserID         int
	ApplicationID  int64
	OrganizationID int
	Page           int
	PageSize       int
}
type ApplicationOverview struct {
	Asset     AssetEntity
	Websites  []ApplicationWebsite
	TotalSize int64
	Page      int
}

func (f *Facade) ApplicationOverview(ctx context.Context, input ApplicationOverviewInput) (*ApplicationOverview, error) {
	if input.ApplicationID <= 0 || input.OrganizationID < 0 || input.OrganizationID > 2147483647 || input.Page < 0 || input.Page > 1000000 || input.PageSize < 0 || input.PageSize > 100 {
		return nil, ErrInvalidQuery
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PageSize == 0 {
		input.PageSize = 50
	}
	asset, err := f.List(ctx, ListInput{UserID: input.UserID, AssetID: input.ApplicationID, AssetType: "app", OrganizationID: input.OrganizationID, PageSize: 1})
	if err != nil {
		return nil, err
	}
	if len(asset.Items) == 0 {
		return nil, ErrApplicationNotFound
	}
	websites, total, err := f.store.ListApplicationWebsites(ctx, ApplicationWebsiteQuery{TenantID: DefaultTenantID, ApplicationID: input.ApplicationID, OrganizationID: input.OrganizationID, Offset: (input.Page - 1) * input.PageSize, Limit: input.PageSize})
	if err != nil {
		return nil, err
	}
	if websites == nil {
		websites = []ApplicationWebsite{}
	}
	return &ApplicationOverview{Asset: asset.Items[0], Websites: websites, TotalSize: total, Page: input.Page}, nil
}
