package application

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	artexdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/traffic"
	"github.com/yyhuni/lunafox/server/internal/aitraffic"
)

type CredentialTrafficDetail struct {
	Reference   artexdb.CredentialTraffic `json:"reference"`
	Request     string                    `json:"req,omitempty"`
	Response    string                    `json:"resp,omitempty"`
	Unavailable bool                      `json:"unavailable"`
}
type CredentialTrafficDetailFacade struct {
	intelligence *IntelligenceFacade
	access       TaskTrafficAuthorizer
	manager      TaskTrafficManager
}

func NewCredentialTrafficDetailFacade(intelligence *IntelligenceFacade, access TaskTrafficAuthorizer, manager TaskTrafficManager) *CredentialTrafficDetailFacade {
	return &CredentialTrafficDetailFacade{intelligence: intelligence, access: access, manager: manager}
}
func (f *CredentialTrafficDetailFacade) Detail(ctx context.Context, user int, taskID, intelID int64, trafficID string) (CredentialTrafficDetail, error) {
	if taskID <= 0 {
		return CredentialTrafficDetail{}, ErrInvalidInput
	}
	return f.detail(ctx, user, intelID, trafficID, func() (IntelligencePage, error) {
		return f.intelligence.List(ctx, user, taskID, IntelligenceFilter{ID: intelID, Limit: 1})
	})
}
func (f *CredentialTrafficDetailFacade) DetailAsset(ctx context.Context, user int, assetID, intelID int64, trafficID string) (CredentialTrafficDetail, error) {
	if assetID <= 0 {
		return CredentialTrafficDetail{}, ErrInvalidInput
	}
	return f.detail(ctx, user, intelID, trafficID, func() (IntelligencePage, error) {
		return f.intelligence.ListAsset(ctx, user, assetID, IntelligenceFilter{ID: intelID, Limit: 1})
	})
}
func (f *CredentialTrafficDetailFacade) detail(ctx context.Context, user int, intelID int64, trafficID string, read func() (IntelligencePage, error)) (CredentialTrafficDetail, error) {
	var out CredentialTrafficDetail
	if f == nil || f.intelligence == nil || f.access == nil || f.manager == nil || user <= 0 || intelID <= 0 || trafficID == "" || len(trafficID) > 128 || !utf8.ValidString(trafficID) || strings.ContainsRune(trafficID, '\x00') {
		return out, ErrInvalidInput
	}
	load := func() (TaskIntelligence, error) {
		page, err := read()
		if err != nil {
			return TaskIntelligence{}, err
		}
		if len(page.Items) != 1 || page.Items[0].Credentials == nil {
			return TaskIntelligence{}, ErrTaskNotFound
		}
		return page.Items[0], nil
	}
	item, err := load()
	if err != nil {
		return out, err
	}
	foundRef := false
	for _, ref := range item.Credentials.Traffic {
		if ref.ID == trafficID {
			out.Reference = ref
			foundRef = true
			break
		}
	}
	if !foundRef {
		return out, ErrTaskNotFound
	}
	tenant, err := f.access.TrafficTaskTenant(ctx, user, item.TaskID)
	if err != nil {
		return out, err
	}
	found, readErr := f.manager.Read(ctx, aitraffic.Scope{TenantID: tenant, UserID: user, TaskID: item.TaskID}, func(recorder *traffic.Traffic) error {
		var e error
		out.Request, out.Response, e = recorder.Get(trafficID)
		return e
	})
	if errors.Is(readErr, sql.ErrNoRows) || readErr == nil && !found {
		out.Unavailable = true
	} else if readErr != nil {
		return CredentialTrafficDetail{}, readErr
	}
	current, err := load()
	if err != nil {
		return CredentialTrafficDetail{}, err
	}
	still := false
	if current.TaskID == item.TaskID {
		for _, ref := range current.Credentials.Traffic {
			if ref.ID == trafficID {
				still = true
				break
			}
		}
	}
	if !still {
		return CredentialTrafficDetail{}, ErrTaskNotFound
	}
	if _, err = f.access.TrafficTaskTenant(ctx, user, item.TaskID); err != nil {
		return CredentialTrafficDetail{}, err
	}
	return out, nil
}
