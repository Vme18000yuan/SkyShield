package application

import (
	"container/heap"
	"context"
	"errors"
	"sort"
	"strconv"

	"github.com/Autumn-27/artex/traffic"
	"github.com/yyhuni/lunafox/server/internal/aitraffic"
	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
)

type TrafficScope struct {
	Scope aitraffic.Scope
	Name  string
}
type TrafficScopeCatalog interface {
	VisibleTrafficScopes(context.Context, int, []aitraffic.Scope) ([]TrafficScope, error)
}
type GlobalTrafficManager interface {
	TaskTrafficManager
	ExistingScopes(context.Context, string, int) ([]aitraffic.Scope, error)
}
type GlobalTrafficItem struct {
	TaskTrafficItem
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
	SourceName string `json:"source_name"`
}
type GlobalTrafficPage struct {
	Items []GlobalTrafficItem `json:"items"`
	Total int                 `json:"total"`
	Page  int                 `json:"page"`
	Size  int                 `json:"size"`
}
type GlobalTrafficHost struct {
	Host  string `json:"host"`
	Count int    `json:"count"`
}
type GlobalTrafficFacade struct {
	manager GlobalTrafficManager
	catalog TrafficScopeCatalog
	tasks   *TaskTrafficFacade
	chats   *ConversationTrafficFacade
}

func NewGlobalTrafficFacade(manager GlobalTrafficManager, catalog TrafficScopeCatalog, tasks *TaskTrafficFacade, chats *ConversationTrafficFacade) *GlobalTrafficFacade {
	return &GlobalTrafficFacade{manager: manager, catalog: catalog, tasks: tasks, chats: chats}
}

type trafficCursor struct {
	scope              TrafficScope
	rows               []traffic.ExchangeMeta
	index, page, total int
}
type trafficHeap struct {
	items       []*trafficCursor
	sort, order string
}

func (h trafficHeap) Len() int { return len(h.items) }
func (h trafficHeap) Less(i, j int) bool {
	a, b := h.items[i].rows[h.items[i].index], h.items[j].rows[h.items[j].index]
	var x, y int64
	switch h.sort {
	case "status":
		x, y = int64(a.Status), int64(b.Status)
	case "resp_len":
		x, y = int64(a.RespLen), int64(b.RespLen)
	default:
		x, y = a.TS, b.TS
	}
	if x != y {
		if h.order == "asc" {
			return x < y
		}
		return x > y
	}
	if a.ID != b.ID {
		return a.ID > b.ID
	}
	return trafficScopeKey(h.items[i].scope.Scope) > trafficScopeKey(h.items[j].scope.Scope)
}
func (h trafficHeap) Swap(i, j int)   { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *trafficHeap) Push(value any) { h.items = append(h.items, value.(*trafficCursor)) }
func (h *trafficHeap) Pop() any {
	last := len(h.items) - 1
	value := h.items[last]
	h.items[last] = nil
	h.items = h.items[:last]
	return value
}
func trafficScopeKey(scope aitraffic.Scope) string {
	if scope.TaskID > 0 {
		return "task:" + strconv.FormatInt(scope.TaskID, 10)
	}
	return "conversation:" + scope.ConversationID
}
func (f *GlobalTrafficFacade) read(ctx context.Context, user int, scope aitraffic.Scope, visit func(*traffic.Traffic) error) (bool, error) {
	if scope.TaskID > 0 {
		return f.tasks.read(ctx, user, scope.TaskID, visit)
	}
	return f.chats.read(ctx, user, scope.ConversationID, visit)
}
func (f *GlobalTrafficFacade) List(ctx context.Context, user int, filter TaskTrafficFilter) (GlobalTrafficPage, error) {
	out := GlobalTrafficPage{Items: []GlobalTrafficItem{}, Page: filter.Page, Size: filter.Size}
	query, err := trafficPageQuery(filter)
	if err != nil || f == nil || f.manager == nil || f.catalog == nil || f.tasks == nil || f.chats == nil || user <= 0 {
		return out, ErrInvalidInput
	}
	onDisk, err := f.manager.ExistingScopes(ctx, assets.DefaultTenantID, user)
	if err != nil {
		return out, err
	}
	scopes, err := f.catalog.VisibleTrafficScopes(ctx, user, onDisk)
	if err != nil {
		return out, err
	}
	const batch = 25
	queue := &trafficHeap{sort: filter.Sort, order: filter.Order}
	counted := make([]aitraffic.Scope, 0, len(scopes))
	for _, scope := range scopes {
		cursor := &trafficCursor{scope: scope}
		found, err := f.read(ctx, user, scope.Scope, func(store *traffic.Traffic) error {
			var readErr error
			cursor.rows, cursor.total, readErr = store.Page(query, 0, batch)
			return readErr
		})
		if errors.Is(err, ErrTaskNotFound) {
			continue
		}
		if err != nil {
			return out, err
		}
		if found {
			out.Total += cursor.total
			counted = append(counted, scope.Scope)
			if len(cursor.rows) > 0 {
				heap.Push(queue, cursor)
			}
		}
	}
	start, end := filter.Page*filter.Size, (filter.Page+1)*filter.Size
	for position := 0; queue.Len() > 0 && position < end; position++ {
		if err := ctx.Err(); err != nil {
			return GlobalTrafficPage{}, err
		}
		cursor := heap.Pop(queue).(*trafficCursor)
		row := cursor.rows[cursor.index]
		if position >= start {
			item := GlobalTrafficItem{TaskTrafficItem: trafficItem(row), SourceName: cursor.scope.Name}
			if cursor.scope.Scope.TaskID > 0 {
				item.SourceType = "task"
				item.SourceID = strconv.FormatInt(cursor.scope.Scope.TaskID, 10)
			} else {
				item.SourceType = "conversation"
				item.SourceID = cursor.scope.Scope.ConversationID
			}
			out.Items = append(out.Items, item)
		}
		cursor.index++
		if cursor.index >= len(cursor.rows) && (cursor.page+1)*batch < cursor.total {
			cursor.page++
			found, readErr := f.read(ctx, user, cursor.scope.Scope, func(store *traffic.Traffic) error {
				var err error
				cursor.rows, _, err = store.Page(query, cursor.page, batch)
				return err
			})
			if readErr != nil {
				return GlobalTrafficPage{}, readErr
			}
			if !found {
				return GlobalTrafficPage{}, ErrTaskNotFound
			}
			cursor.index = 0
		}
		if cursor.index < len(cursor.rows) {
			heap.Push(queue, cursor)
		}
	}
	if len(counted) > 0 {
		visible, err := f.catalog.VisibleTrafficScopes(ctx, user, counted)
		if err != nil {
			return GlobalTrafficPage{}, err
		}
		if len(visible) != len(counted) {
			return GlobalTrafficPage{}, ErrTaskNotFound
		}
	}
	return out, nil
}

func (f *GlobalTrafficFacade) Hosts(ctx context.Context, user int) ([]GlobalTrafficHost, error) {
	if f == nil || f.manager == nil || f.catalog == nil || f.tasks == nil || f.chats == nil || user <= 0 {
		return nil, ErrInvalidInput
	}
	onDisk, err := f.manager.ExistingScopes(ctx, assets.DefaultTenantID, user)
	if err != nil {
		return nil, err
	}
	scopes, err := f.catalog.VisibleTrafficScopes(ctx, user, onDisk)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	counted := make([]aitraffic.Scope, 0, len(scopes))
	for _, scope := range scopes {
		var hosts []traffic.HostCount
		found, err := f.read(ctx, user, scope.Scope, func(store *traffic.Traffic) error {
			var readErr error
			hosts, readErr = store.Hosts()
			return readErr
		})
		if errors.Is(err, ErrTaskNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if found {
			counted = append(counted, scope.Scope)
			for _, item := range hosts {
				counts[item.Host] += item.Count
			}
		}
	}
	if len(counted) > 0 {
		visible, err := f.catalog.VisibleTrafficScopes(ctx, user, counted)
		if err != nil {
			return nil, err
		}
		if len(visible) != len(counted) {
			return nil, ErrTaskNotFound
		}
	}
	out := make([]GlobalTrafficHost, 0, len(counts))
	for host, count := range counts {
		out = append(out, GlobalTrafficHost{Host: host, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Host < out[j].Host
	})
	return out, nil
}
