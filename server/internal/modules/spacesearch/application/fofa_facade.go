package application

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
)

const credentialScope = "tianqiong/network-search/fofa/v1"

type FofaFacade struct {
	store    Store
	provider Provider
	codec    SecretCodec
	importer AssetImporter
}

func NewFofaFacade(store Store, provider Provider, codec SecretCodec, importer AssetImporter) *FofaFacade {
	return &FofaFacade{store: store, provider: provider, codec: codec, importer: importer}
}
func (f *FofaFacade) Status(ctx context.Context, user int) (*SettingsView, error) {
	access, err := f.store.Access(ctx, user)
	if err != nil {
		return nil, err
	}
	settings, err := f.store.GetSettings(ctx, user)
	if err != nil {
		return nil, err
	}
	view := &SettingsView{CanManage: access.CanManage, CanSearch: access.CanSearch}
	if settings != nil {
		view.Configured = len(settings.EncryptedKey) > 0 && settings.Email != ""
		t := settings.UpdatedAt.UTC()
		view.UpdatedAt = &t
		if access.CanManage {
			view.Email = settings.Email
		}
	}
	return view, nil
}
func (f *FofaFacade) Save(ctx context.Context, user int, email, key string) (*SettingsView, error) {
	access, err := f.store.Access(ctx, user)
	if err != nil {
		return nil, err
	}
	if !access.CanManage {
		return nil, ErrForbidden
	}
	email = strings.TrimSpace(email)
	key = strings.TrimSpace(key)
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 || len(key) > 4096 || strings.ContainsAny(key, "\r\n\x00") {
		return nil, ErrInvalidInput
	}
	settings := Settings{Email: email}
	if key != "" {
		settings.EncryptedKey, err = f.codec.Seal(credentialScope, key)
		if err != nil {
			return nil, err
		}
	} else {
		previous, readErr := f.store.GetSettings(ctx, user)
		if readErr != nil {
			return nil, readErr
		}
		if previous == nil || len(previous.EncryptedKey) == 0 {
			return nil, ErrNotConfigured
		}
	}
	if err = f.store.SaveSettings(ctx, user, settings, false); err != nil {
		return nil, err
	}
	return f.Status(ctx, user)
}
func (f *FofaFacade) Clear(ctx context.Context, user int) error {
	return f.store.SaveSettings(ctx, user, Settings{}, true)
}
func (f *FofaFacade) credentials(ctx context.Context, user int) (Credentials, error) {
	settings, err := f.store.GetSettings(ctx, user)
	if err != nil {
		return Credentials{}, err
	}
	if settings == nil || settings.Email == "" || len(settings.EncryptedKey) == 0 {
		return Credentials{}, ErrNotConfigured
	}
	key, err := f.codec.Open(credentialScope, settings.EncryptedKey)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{Email: settings.Email, APIKey: key}, nil
}
func (f *FofaFacade) Test(ctx context.Context, user int) error {
	access, err := f.store.Access(ctx, user)
	if err != nil {
		return err
	}
	if !access.CanManage {
		return ErrForbidden
	}
	credentials, err := f.credentials(ctx, user)
	if err != nil {
		return err
	}
	return f.provider.Test(ctx, credentials)
}
func (f *FofaFacade) Search(ctx context.Context, user int, input SearchInput) (*SearchPage, error) {
	input.Query = strings.TrimSpace(input.Query)
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PageSize == 0 {
		input.PageSize = 50
	}
	if user <= 0 || input.Query == "" || len(input.Query) > 8192 || input.Page < 1 || input.Page > 1000 || input.PageSize < 1 || input.PageSize > 100 {
		return nil, ErrInvalidInput
	}
	access, err := f.store.Access(ctx, user)
	if err != nil {
		return nil, err
	}
	if !access.CanSearch {
		return nil, ErrForbidden
	}
	credentials, err := f.credentials(ctx, user)
	if err != nil {
		return nil, err
	}
	result, err := f.provider.Search(ctx, credentials, input)
	if err != nil {
		return nil, err
	}
	if result.Total < 0 || len(result.Results) > input.PageSize {
		return nil, ErrProvider
	}
	for i := range result.Results {
		result.Results[i] = NormalizeRecord(result.Results[i])
		result.Results[i].RowID = i
	}
	now := time.Now().UTC()
	page := SearchPage{ID: uuid.NewString(), Query: input.Query, Page: input.Page, PageSize: input.PageSize, Full: input.Full, Total: result.Total, Results: result.Results, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if page.Results == nil {
		page.Results = []Record{}
	}
	page.HasNext = len(page.Results) == input.PageSize && int64(input.Page)*int64(input.PageSize) < result.Total && input.Page < 1000
	if err = f.store.SaveSearch(ctx, user, page); err != nil {
		return nil, err
	}
	return &page, nil
}
func (f *FofaFacade) Import(ctx context.Context, input ImportInput) (*ImportResult, error) {
	id, err := uuid.Parse(input.SearchID)
	if err != nil || id.String() != input.SearchID || input.UserID <= 0 || input.OrganizationID <= 0 || input.TargetID < 0 || len(input.Rows) == 0 || len(input.Rows) > 100 {
		return nil, ErrInvalidInput
	}
	seen := map[int]bool{}
	for _, row := range input.Rows {
		if row < 0 || row >= 100 || seen[row] {
			return nil, ErrInvalidInput
		}
		seen[row] = true
	}
	access, err := f.store.Access(ctx, input.UserID)
	if err != nil {
		return nil, err
	}
	if !access.CanSearch {
		return nil, ErrForbidden
	}
	result := &ImportResult{Results: []ImportRowResult{}, Errors: []ImportRowError{}}
	assets := map[string]bool{}
	for _, row := range input.Rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		selection := ImportSelection{UserID: input.UserID, SearchID: input.SearchID, RowID: row, OrganizationID: input.OrganizationID, TargetID: input.TargetID}
		item, importErr := f.store.ImportRecord(ctx, selection, func(txCtx context.Context, record Record, query string) (ImportRowResult, error) {
			return f.importer.Import(txCtx, ImportCommand{Selection: selection, Record: record, Query: query})
		})
		if importErr != nil {
			if errors.Is(importErr, ErrSearchNotFound) || errors.Is(importErr, ErrForbidden) {
				return nil, importErr
			}
			message := "资产同步失败"
			if errors.Is(importErr, ErrDestination) {
				message = "组织或目标不存在、已删除，或目标无法用于该结果"
			} else if errors.Is(importErr, ErrInvalidInput) {
				message = "此条 FOFA 结果缺少有效主机、IP 或端口"
			} else {
				var safe *ImportError
				if errors.As(importErr, &safe) {
					message = safe.Message
				}
			}
			result.Errors = append(result.Errors, ImportRowError{RowID: row, Message: message})
			result.FailedRows++
			continue
		}
		result.Results = append(result.Results, item)
		result.ImportedRows++
		for _, asset := range item.AssetIDs {
			assets[asset] = true
		}
	}
	result.AssetCount = len(assets)
	return result, nil
}

type ImportError struct{ Message string }

func (e *ImportError) Error() string { return e.Message }
