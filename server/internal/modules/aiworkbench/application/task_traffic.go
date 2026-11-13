package application

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Autumn-27/artex/traffic"
	"github.com/yyhuni/lunafox/server/internal/aitraffic"
)

type TaskTrafficAuthorizer interface {
	TrafficTaskTenant(context.Context, int, int64) (string, error)
}
type TaskTrafficManager interface {
	Read(context.Context, aitraffic.Scope, func(*traffic.Traffic) error) (bool, error)
}
type TaskTrafficFacade struct {
	access  TaskTrafficAuthorizer
	manager TaskTrafficManager
}

func NewTaskTrafficFacade(access TaskTrafficAuthorizer, manager TaskTrafficManager) *TaskTrafficFacade {
	return &TaskTrafficFacade{access: access, manager: manager}
}

type TaskTrafficItem struct {
	ID             string    `json:"id"`
	Host           string    `json:"host"`
	Method         string    `json:"method"`
	URL            string    `json:"url"`
	Status         int       `json:"status"`
	ContentType    string    `json:"content_type"`
	ResponseLength int       `json:"response_length"`
	CapturedAt     time.Time `json:"captured_at"`
}
type TaskTrafficPage struct {
	Items []TaskTrafficItem `json:"items"`
	Total int               `json:"total"`
	Page  int               `json:"page"`
	Size  int               `json:"size"`
}
type TaskTrafficFilter struct {
	Host, Method, Query, Body, Path, Status, Sort, Order string
	RespMin, RespMax                                     *int64
	Page, Size                                           int
}

func (f *TaskTrafficFacade) read(ctx context.Context, user int, taskID int64, visit func(*traffic.Traffic) error) (bool, error) {
	if f == nil || f.access == nil || f.manager == nil || user <= 0 || taskID <= 0 {
		return false, ErrInvalidInput
	}
	tenant, err := f.access.TrafficTaskTenant(ctx, user, taskID)
	if err != nil {
		return false, err
	}
	found, err := f.manager.Read(ctx, aitraffic.Scope{TenantID: tenant, UserID: user, TaskID: taskID}, visit)
	if err != nil {
		return false, err
	}
	current, err := f.access.TrafficTaskTenant(ctx, user, taskID)
	if err != nil || current != tenant {
		if err == nil {
			err = ErrTaskNotFound
		}
		return false, err
	}
	return found, nil
}
func (f *TaskTrafficFacade) List(ctx context.Context, user int, taskID int64, filter TaskTrafficFilter) (TaskTrafficPage, error) {
	return listTraffic(filter, func(visit func(*traffic.Traffic) error) (bool, error) {
		return f.read(ctx, user, taskID, visit)
	})
}

type trafficRead func(func(*traffic.Traffic) error) (bool, error)

func listTraffic(filter TaskTrafficFilter, read trafficRead) (TaskTrafficPage, error) {
	out := TaskTrafficPage{Items: []TaskTrafficItem{}, Page: filter.Page, Size: filter.Size}
	query, err := trafficPageQuery(filter)
	if err != nil {
		return out, err
	}
	_, err = read(func(store *traffic.Traffic) error {
		rows, total, err := store.Page(query, filter.Page, filter.Size)
		if err != nil {
			return err
		}
		out.Total = total
		for _, row := range rows {
			out.Items = append(out.Items, trafficItem(row))
		}
		return nil
	})
	return out, err
}
func trafficPageQuery(filter TaskTrafficFilter) (traffic.PageQuery, error) {
	if filter.Page < 0 || filter.Page > 10000 || filter.Size < 1 || filter.Size > 100 {
		return traffic.PageQuery{}, ErrInvalidInput
	}
	for _, text := range []string{filter.Host, filter.Method, filter.Query, filter.Body, filter.Path, filter.Status, filter.Sort, filter.Order} {
		if len(text) > 500 || !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') {
			return traffic.PageQuery{}, ErrInvalidInput
		}
	}
	if filter.Sort != "" && filter.Sort != "ts" && filter.Sort != "status" && filter.Sort != "resp_len" || filter.Order != "" && filter.Order != "asc" && filter.Order != "desc" {
		return traffic.PageQuery{}, ErrInvalidInput
	}
	min, max := int64(-1), int64(-1)
	if filter.RespMin != nil {
		min = *filter.RespMin
		if min < 0 {
			return traffic.PageQuery{}, ErrInvalidInput
		}
	}
	if filter.RespMax != nil {
		max = *filter.RespMax
		if max < 0 || min > max {
			return traffic.PageQuery{}, ErrInvalidInput
		}
	}
	return traffic.PageQuery{Host: filter.Host, Method: filter.Method, Query: filter.Query, Body: filter.Body, Path: filter.Path, Status: filter.Status, Sort: filter.Sort, Order: filter.Order, RespMin: min, RespMax: max}, nil
}
func trafficItem(row traffic.ExchangeMeta) TaskTrafficItem {
	return TaskTrafficItem{ID: row.ID, Host: row.Host, Method: row.Method, URL: row.URL, Status: row.Status, ContentType: row.ContentType, ResponseLength: row.RespLen, CapturedAt: time.Unix(row.TS, 0).UTC()}
}
func (f *TaskTrafficFacade) Exchange(ctx context.Context, user int, taskID int64, id string) (string, string, error) {
	return trafficExchange(id, func(visit func(*traffic.Traffic) error) (bool, error) {
		return f.read(ctx, user, taskID, visit)
	})
}
func trafficExchange(id string, read trafficRead) (string, string, error) {
	if id == "" || len(id) > 128 || !utf8.ValidString(id) || strings.ContainsRune(id, '\x00') {
		return "", "", ErrInvalidInput
	}
	var request, response string
	found, err := read(func(store *traffic.Traffic) error { var e error; request, response, e = store.Get(id); return e })
	if errors.Is(err, sql.ErrNoRows) || err == nil && !found {
		return "", "", ErrTaskNotFound
	}
	return request, response, err
}
func (f *TaskTrafficFacade) Blob(ctx context.Context, user int, taskID int64, hash string, offset, length int64) ([]byte, int64, error) {
	return trafficBlob(hash, offset, length, func(visit func(*traffic.Traffic) error) (bool, error) {
		return f.read(ctx, user, taskID, visit)
	})
}
func trafficBlob(hash string, offset, length int64, read trafficRead) ([]byte, int64, error) {
	if len(hash) != 64 || hash != strings.ToLower(hash) || offset < 0 || length < 1 || length > 8192 {
		return nil, 0, ErrInvalidInput
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return nil, 0, ErrInvalidInput
	}
	var data []byte
	var total int64
	found, err := read(func(store *traffic.Traffic) error {
		var e error
		data, total, e = store.BlobRange(hash, offset, length)
		return e
	})
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, os.ErrNotExist) || err == nil && !found {
		return nil, 0, ErrTaskNotFound
	}
	return data, total, err
}
