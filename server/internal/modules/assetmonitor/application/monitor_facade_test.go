package application

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/yyhuni/lunafox/server/internal/geolocation"
)

type monitorFixtureStore struct {
	mu       sync.Mutex
	reads    int
	pending  []string
	saved    chan geolocation.Location
	hosts    []string
	dnsSaved chan string
}

func (s *monitorFixtureStore) Read(context.Context, int, []int) (*Snapshot, error) {
	s.mu.Lock()
	s.reads++
	s.mu.Unlock()
	return &Snapshot{PendingIPs: append([]string(nil), s.pending...), PendingHosts: append([]string(nil), s.hosts...)}, nil
}
func (s *monitorFixtureStore) ClaimIP(_ context.Context, ip string) (bool, error) {
	_, ok := geolocation.NormalizePublicIP(ip)
	return ok, nil
}
func (s *monitorFixtureStore) SaveLocation(_ context.Context, l geolocation.Location) error {
	s.saved <- l
	return nil
}
func (s *monitorFixtureStore) ClaimHost(_ context.Context, _ string) (bool, error) { return true, nil }
func (s *monitorFixtureStore) SaveHostResolution(_ context.Context, host, ip string, _ time.Time) error {
	s.dnsSaved <- host + "=" + ip
	return nil
}

type monitorFixtureDNS struct {
	mu    sync.Mutex
	calls []string
}

func (d *monitorFixtureDNS) ResolveHost(_ context.Context, host string) (string, error) {
	d.mu.Lock()
	d.calls = append(d.calls, host)
	d.mu.Unlock()
	if host == "fake.example.com" {
		return "198.18.0.1", nil
	}
	return "8.8.8.8", nil
}

type monitorFixtureGeo struct {
	mu        sync.Mutex
	submitted []string
}

func (g *monitorFixtureGeo) SubmitIP(_ context.Context, ip string, done geolocation.LookupCompletion) error {
	g.mu.Lock()
	g.submitted = append(g.submitted, ip)
	g.mu.Unlock()
	done(geolocation.LookupOutcome{Location: &geolocation.Location{ObservedIP: ip, Latitude: 30, Longitude: 110, ResolvedAt: time.Now(), ProviderKey: geolocation.FreeIPAPIProviderKey}})
	return nil
}

func TestMonitorSelectionFailsBeforeReadingData(t *testing.T) {
	store := &monitorFixtureStore{}
	f := NewFacade(store, nil)
	for _, ids := range [][]int{nil, {1, 1}, {0}, {-1}, make([]int, 51)} {
		if _, err := f.Read(context.Background(), 1, ids); err != ErrInvalidSelection {
			t.Fatalf("invalid selection accepted: %v %v", ids, err)
		}
	}
	if store.reads != 0 {
		t.Fatal("invalid scope reached repository")
	}
}
func TestMonitorEnrichmentIsBoundedAndNeverSubmitsPrivateIPs(t *testing.T) {
	pending := []string{"10.0.0.1", "127.0.0.1", "invalid", "8.8.8.8", "8.8.8.8"}
	for i := 1; i <= 24; i++ {
		pending = append(pending, fmt.Sprintf("8.8.4.%d", i))
	}
	store := &monitorFixtureStore{pending: pending, saved: make(chan geolocation.Location, 32)}
	geo := &monitorFixtureGeo{}
	f := NewFacade(store, geo)
	ctx, cancel := context.WithCancel(context.Background())
	f.Start(ctx)
	defer cancel()
	snapshot, err := f.Read(context.Background(), 1, []int{2, 1})
	if err != nil || snapshot.PendingIPs != nil {
		t.Fatalf("snapshot exposed enrichment internals: %+v %v", snapshot, err)
	}
	for i := 0; i < 16; i++ {
		select {
		case <-store.saved:
		case <-time.After(time.Second):
			t.Fatal("enrichment did not finish")
		}
	}
	cancel()
	select {
	case <-f.Done():
	case <-time.After(time.Second):
		t.Fatal("enrichment lifecycle did not stop")
	}
	geo.mu.Lock()
	defer geo.mu.Unlock()
	if len(geo.submitted) != 16 {
		t.Fatalf("unbounded enrichment: %v", geo.submitted)
	}
	seen := map[string]bool{}
	for _, ip := range geo.submitted {
		if _, ok := geolocation.NormalizePublicIP(ip); !ok || seen[ip] {
			t.Fatalf("private or duplicate IP submitted: %s", ip)
		}
		seen[ip] = true
	}
}

func TestMonitorDNSResolutionIsBoundedAndUsesOnlyPublicAddresses(t *testing.T) {
	hosts := []string{"fake.example.com", "good.example.com", "good.example.com"}
	for i := 0; i < 20; i++ {
		hosts = append(hosts, fmt.Sprintf("host%d.example.com", i))
	}
	store := &monitorFixtureStore{hosts: hosts, pending: []string{"198.18.0.3"}, saved: make(chan geolocation.Location, 32), dnsSaved: make(chan string, 32)}
	geo := &monitorFixtureGeo{}
	dns := &monitorFixtureDNS{}
	f := NewFacade(store, geo, dns)
	ctx, cancel := context.WithCancel(context.Background())
	f.Start(ctx)
	snapshot, err := f.Read(context.Background(), 1, []int{1})
	if err != nil || snapshot.PendingIPs != nil || snapshot.PendingHosts != nil {
		t.Fatalf("snapshot leaked candidates: %+v %v", snapshot, err)
	}
	for i := 0; i < 15; i++ {
		select {
		case result := <-store.dnsSaved:
			if result == "fake.example.com=198.18.0.1" {
				t.Fatal("fake IP saved as DNS evidence")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("DNS enrichment did not finish")
		}
	}
	cancel()
	<-f.Done()
	dns.mu.Lock()
	defer dns.mu.Unlock()
	if len(dns.calls) != 16 {
		t.Fatalf("DNS attempts = %d, want 16", len(dns.calls))
	}
	geo.mu.Lock()
	defer geo.mu.Unlock()
	if len(geo.submitted) != 1 || geo.submitted[0] != "8.8.8.8" {
		t.Fatalf("geolocation submitted fake or duplicate IPs: %v", geo.submitted)
	}
}
