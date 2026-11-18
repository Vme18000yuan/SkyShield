package application

import (
	"context"
	"strings"
)

type InventoryItem struct {
	Asset *AssetEntity
	Port  *PortAsset
}
type InventoryResult struct {
	Items     []InventoryItem
	TotalSize int64
	Page      int
}

// ListInventory puts real entity records and projected host:port observations
// into one pageable inventory, without inventing an asset ID for a port.
func (f *Facade) ListInventory(ctx context.Context, input PortListInput) (*InventoryResult, error) {
	if f == nil || f.store == nil || input.UserID <= 0 {
		return nil, ErrForbidden
	}
	input.Query = strings.TrimSpace(input.Query)
	if len(input.Query) > 200 || input.OrganizationID < 0 || input.OrganizationID > 2147483647 || input.RootDomainID < 0 || input.Page < 0 || input.Page > 1000000 || input.PageSize < 0 || input.PageSize > 100 {
		return nil, ErrInvalidQuery
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PageSize == 0 {
		input.PageSize = 50
	}
	member, err := f.store.IsActiveTenantMember(ctx, DefaultTenantID, input.UserID)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrForbidden
	}
	entities := AssetQuery{HideCDN: input.HideCDN, TenantID: DefaultTenantID, OrganizationID: input.OrganizationID, RootDomainID: input.RootDomainID, Query: input.Query, ExcludeNonWebServices: true}
	entityTotal, err := f.store.CountAssets(ctx, entities)
	if err != nil {
		return nil, err
	}
	ports := PortQuery{HideCDN: input.HideCDN, TenantID: DefaultTenantID, OrganizationID: input.OrganizationID, RootDomainID: input.RootDomainID, Query: input.Query, Limit: 0}
	_, portTotal, err := f.store.ListPorts(ctx, ports)
	if err != nil {
		return nil, err
	}
	offset := (input.Page - 1) * input.PageSize
	result := &InventoryResult{Items: []InventoryItem{}, TotalSize: entityTotal + portTotal, Page: input.Page}
	if int64(offset) < entityTotal {
		entities.Offset = offset
		entities.Limit = input.PageSize
		items, e := f.store.ListAssets(ctx, entities)
		if e != nil {
			return nil, e
		}
		for i := range items {
			items[i].DisplayType = AssetDisplayType(items[i].AssetType, items[i].Label)
			result.Items = append(result.Items, InventoryItem{Asset: &items[i]})
		}
	}
	if remaining := input.PageSize - len(result.Items); remaining > 0 && int64(offset)+int64(len(result.Items)) >= entityTotal {
		ports.Offset = int(int64(offset) + int64(len(result.Items)) - entityTotal)
		ports.Limit = remaining
		items, _, e := f.store.ListPorts(ctx, ports)
		if e != nil {
			return nil, e
		}
		classifyPorts(items)
		for i := range items {
			result.Items = append(result.Items, InventoryItem{Port: &items[i]})
		}
	}
	return result, nil
}
