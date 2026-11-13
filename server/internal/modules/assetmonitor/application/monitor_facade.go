package application

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/yyhuni/lunafox/server/internal/geolocation"
)

type Facade struct {
	store Store
	geo   LocationCoordinator
	dns   DNSResolver
	mu    sync.RWMutex
	root  context.Context
	once  sync.Once
	work  chan enrichmentRequest
	done  chan struct{}
}

type enrichmentRequest struct{ ips, hosts []string }

func NewFacade(store Store, geo LocationCoordinator, resolvers ...DNSResolver) *Facade {
	var dns DNSResolver
	if len(resolvers) > 0 {
		dns = resolvers[0]
	}
	return &Facade{store: store, geo: geo, dns: dns, work: make(chan enrichmentRequest, 1), done: make(chan struct{})}
}

// Start runs before requests are admitted. Enrichment ends with this lifecycle.
func (f *Facade) Start(ctx context.Context) {
	f.once.Do(func() {
		f.mu.Lock()
		f.root = ctx
		f.mu.Unlock()
		go func() {
			defer close(f.done)
			for {
				select {
				case <-ctx.Done():
					return
				case candidates := <-f.work:
					if ctx.Err() == nil {
						f.enrich(ctx, candidates)
					}
				}
			}
		}()
	})
}
func (f *Facade) Done() <-chan struct{} { return f.done }
func (f *Facade) Read(ctx context.Context, user int, organizations []int) (*Snapshot, error) {
	if user <= 0 {
		return nil, ErrForbidden
	}
	if len(organizations) == 0 || len(organizations) > 50 {
		return nil, ErrInvalidSelection
	}
	ids := append([]int(nil), organizations...)
	sort.Ints(ids)
	for i, id := range ids {
		if id <= 0 || id > 2147483647 || (i > 0 && ids[i-1] == id) {
			return nil, ErrInvalidSelection
		}
	}
	snapshot, err := f.store.Read(ctx, user, ids)
	if err != nil {
		return nil, err
	}
	f.mu.RLock()
	root := f.root
	f.mu.RUnlock()
	if f.geo != nil && root != nil && root.Err() == nil {
		select {
		case f.work <- enrichmentRequest{ips: append([]string(nil), snapshot.PendingIPs...), hosts: append([]string(nil), snapshot.PendingHosts...)}:
		default:
		}
	}
	snapshot.PendingIPs = nil
	snapshot.PendingHosts = nil
	return snapshot, nil
}
func (f *Facade) enrich(root context.Context, request enrichmentRequest) {
	submitted := 0
	seen := map[string]bool{}
	submit := func(candidate string) {
		if root.Err() != nil || submitted == 16 {
			return
		}
		ip, ok := geolocation.NormalizePublicIP(candidate)
		if !ok {
			claimCtx, cancel := context.WithTimeout(root, time.Second)
			_, _ = f.store.ClaimIP(claimCtx, candidate)
			cancel()
			return
		}
		if seen[ip] {
			return
		}
		seen[ip] = true
		claimCtx, cancel := context.WithTimeout(root, time.Second)
		claimed, claimErr := f.store.ClaimIP(claimCtx, ip)
		cancel()
		if claimErr != nil || !claimed {
			return
		}
		submitted++
		_ = f.geo.SubmitIP(root, ip, func(result geolocation.LookupOutcome) {
			if result.Location == nil || root.Err() != nil {
				return
			}
			saveCtx, cancel := context.WithTimeout(root, 3*time.Second)
			defer cancel()
			_ = f.store.SaveLocation(saveCtx, *result.Location)
		})
	}
	for _, candidate := range request.ips {
		submit(candidate)
	}
	if f.dns == nil {
		return
	}
	seenHosts := map[string]bool{}
	resolved := 0
	for _, host := range request.hosts {
		if root.Err() != nil || resolved == 16 {
			break
		}
		if seenHosts[host] {
			continue
		}
		seenHosts[host] = true
		claimCtx, cancel := context.WithTimeout(root, time.Second)
		claimed, err := f.store.ClaimHost(claimCtx, host)
		cancel()
		if err != nil || !claimed {
			continue
		}
		resolved++
		ip, err := f.dns.ResolveHost(root, host)
		if err != nil {
			continue
		}
		ip, ok := geolocation.NormalizePublicIP(ip)
		if !ok {
			continue
		}
		saveCtx, saveCancel := context.WithTimeout(root, time.Second)
		err = f.store.SaveHostResolution(saveCtx, host, ip, time.Now().UTC())
		saveCancel()
		if err == nil {
			submit(ip)
		}
	}
}
