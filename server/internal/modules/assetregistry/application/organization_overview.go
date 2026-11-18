package application

import (
	"context"
	"errors"
)

var ErrOrganizationNotFound = errors.New("organization not found")

type OrganizationAssetOverview struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	RootDomains int64  `json:"rootDomains"`
	Subdomains  int64  `json:"subdomains"`
	IPs         int64  `json:"ips"`
}

func (f *Facade) OrganizationOverview(ctx context.Context, user, org int, hideCDN bool) (*OrganizationAssetOverview, error) {
	if f == nil || f.store == nil || user <= 0 {
		return nil, ErrForbidden
	}
	if org <= 0 || org > 2147483647 {
		return nil, ErrInvalidQuery
	}
	member, err := f.store.IsActiveTenantMember(ctx, DefaultTenantID, user)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrForbidden
	}
	store, ok := f.store.(interface {
		ReadOrganizationOverview(context.Context, AssetQuery) (*OrganizationAssetOverview, error)
	})
	if !ok {
		return nil, errors.New("organization asset overview unavailable")
	}
	return store.ReadOrganizationOverview(ctx, AssetQuery{TenantID: DefaultTenantID, OrganizationID: org, HideCDN: hideCDN})
}
