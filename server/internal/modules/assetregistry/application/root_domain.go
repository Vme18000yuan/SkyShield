package application

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	contractresults "github.com/yyhuni/lunafox/contracts/results"
	"github.com/yyhuni/lunafox/server/internal/pkg"
	"go.uber.org/zap"
	"golang.org/x/net/publicsuffix"
)

// RegistrableRootDomain derives an eTLD+1 using the public suffix list,
// including private suffixes such as github.io. It does not authorize or
// create a scan target for the resulting parent domain.
func RegistrableRootDomain(kind, value string) (string, bool) {
	value = strings.TrimSpace(value)
	switch kind {
	case "target", "subdomain":
	case "service", "endpoint":
		if host, err := contractresults.DeriveObservedAssetURLHost(value); err == nil {
			value = host
		} else if kind == "service" {
			if host, _, splitErr := net.SplitHostPort(value); splitErr == nil {
				value = host
			} else {
				return "", false
			}
		} else {
			return "", false
		}
	default:
		return "", false
	}
	domain, ok := contractresults.NormalizeSubdomainDNSName(value)
	if !ok {
		return "", false
	}
	root, err := publicsuffix.EffectiveTLDPlusOne(domain)
	if err != nil || root == "" {
		return "", false
	}
	return root, true
}

type RootDomainStore interface {
	ProcessRootDomainBatch(context.Context, int) (bool, error)
}

// RootDomainController continuously consumes the durable change queue while
// progressing through pre-existing targets and assets in bounded batches.
type RootDomainController struct {
	store RootDomainStore
	once  sync.Once
	done  chan struct{}
}

func NewRootDomainController(store RootDomainStore) *RootDomainController {
	return &RootDomainController{store: store, done: make(chan struct{})}
}
func (c *RootDomainController) Start(ctx context.Context) { c.once.Do(func() { go c.run(ctx) }) }
func (c *RootDomainController) Done() <-chan struct{}     { return c.done }
func (c *RootDomainController) run(ctx context.Context) {
	defer close(c.done)
	for ctx.Err() == nil {
		worked, err := c.store.ProcessRootDomainBatch(ctx, 200)
		delay := time.Second
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			pkg.Error("Root domain projection failed", zap.Error(err))
			delay = 5 * time.Second
		} else if worked {
			delay = 100 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
