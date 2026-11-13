package repository

import (
	"context"
	"net"
	"net/url"

	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	blacklist "github.com/yyhuni/lunafox/server/internal/modules/blacklist/domain"
	app "github.com/yyhuni/lunafox/server/internal/modules/httprepeater/application"
	security "github.com/yyhuni/lunafox/server/internal/modules/security/domain"
	"gorm.io/gorm"
)

type AccessStore struct {
	db       *gorm.DB
	patterns func(context.Context, int) ([]string, error)
}

func NewAccessStore(db *gorm.DB, patterns func(context.Context, int) ([]string, error)) *AccessStore {
	return &AccessStore{db, patterns}
}
func (s *AccessStore) Permit(ctx context.Context, user int, urls []string) (app.AddressGuard, error) {
	var allowed bool
	err := s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM tenant_membership m JOIN tenant t ON t.id=m.tenant_id JOIN auth_user u ON u.id=m.user_id WHERE m.tenant_id=?::uuid AND m.user_id=? AND m.role IN ('admin','analyst') AND t.status='active' AND u.is_active)`, assets.DefaultTenantID, user).Scan(&allowed).Error
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, app.ErrForbidden
	}
	patterns, err := s.patterns(ctx, 0)
	if err != nil {
		return nil, err
	}
	patterns = append([]string(nil), patterns...)
	var targets []security.TargetRef
	if err = s.db.WithContext(ctx).Table("target").Select("id,name,type").Where("deleted_at IS NULL").Find(&targets).Error; err != nil {
		return nil, err
	}
	for _, target := range targets {
		for _, raw := range urls {
			if security.IsURLMatchTarget(raw, target) {
				extra, e := s.patterns(ctx, target.ID)
				if e != nil {
					return nil, e
				}
				patterns = append(patterns, extra...)
				break
			}
		}
	}
	patterns, err = blacklist.CanonicalizeEffectivePatterns(patterns)
	if err != nil {
		return nil, err
	}
	matcher, err := blacklist.CompileMatcher(patterns)
	if err != nil {
		return nil, err
	}
	for _, raw := range urls {
		u, e := url.Parse(raw)
		if e != nil || matcher.MatchesURL(raw) || (u != nil && matcher.MatchesHostname(u.Hostname())) {
			return nil, app.ErrBlocked
		}
	}
	return func(address string) error {
		if matcher.MatchesHostname(address) {
			return app.ErrBlocked
		}
		// A hostname can resolve into a Target defined by an IP or CIDR. Apply
		// that Target's policy too, before the checked IP is pinned for dialing.
		for _, target := range targets {
			if target.Type == "domain" || !security.IsURLMatchTarget("http://"+net.JoinHostPort(address, "80"), target) {
				continue
			}
			extra, e := s.patterns(ctx, target.ID)
			if e != nil {
				return e
			}
			check, e := blacklist.CompileMatcher(extra)
			if e != nil {
				return e
			}
			if check.MatchesHostname(address) {
				return app.ErrBlocked
			}
			for _, raw := range urls {
				if check.MatchesURL(raw) {
					return app.ErrBlocked
				}
			}
		}
		return nil
	}, nil
}
