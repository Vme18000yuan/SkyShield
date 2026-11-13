package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	assets "github.com/yyhuni/lunafox/server/internal/modules/assetregistry/application"
	blacklist "github.com/yyhuni/lunafox/server/internal/modules/blacklist/domain"
	app "github.com/yyhuni/lunafox/server/internal/modules/httprepeater/application"
	repo "github.com/yyhuni/lunafox/server/internal/modules/httprepeater/repository"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func verifyHTTPRepeaterAccess(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var user, domain, cidr int
	if err := db.QueryRowContext(ctx, `INSERT INTO auth_user(username,password,is_active) VALUES('repeater-viewer','unused',TRUE) RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE tenant_membership SET role='viewer' WHERE user_id=$1 AND tenant_id=$2::uuid`, user, assets.DefaultTenantID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('repeater-policy.example','domain') RETURNING id`).Scan(&domain); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO target(name,type) VALUES('10.85.0.0/16','cidr') RETURNING id`).Scan(&cidr); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	store := repo.NewAccessStore(orm, func(_ context.Context, target int) ([]string, error) {
		calls++
		p := []string{"global-blocked.example", "192.0.2.8"}
		if target == domain {
			p = append(p, "blocked.repeater-policy.example")
		}
		if target == cidr {
			p = append(p, "blocked-alias.example", "10.85.3.0/24")
		}
		return blacklist.CanonicalizeEffectivePatterns(p)
	})
	if _, err = store.Permit(ctx, user, []string{"https://allowed.repeater-policy.example/api"}); !errors.Is(err, app.ErrForbidden) || calls != 0 {
		t.Fatalf("viewer allowed: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE tenant_membership SET role='analyst' WHERE user_id=$1 AND tenant_id=$2::uuid`, user, assets.DefaultTenantID); err != nil {
		t.Fatal(err)
	}
	guard, err := store.Permit(ctx, user, []string{"https://allowed.repeater-policy.example/api"})
	if err != nil {
		t.Fatalf("canonical policy merge: %v", err)
	}
	if err = guard("192.0.2.8"); !errors.Is(err, app.ErrBlocked) {
		t.Fatal("resolved IP escaped global blacklist")
	}
	for _, raw := range []string{"https://global-blocked.example/", "https://blocked.repeater-policy.example/"} {
		if _, err = store.Permit(ctx, user, []string{raw}); !errors.Is(err, app.ErrBlocked) {
			t.Fatalf("policy bypass for %s: %v", raw, err)
		}
	}
	guard, err = store.Permit(ctx, user, []string{"https://blocked-alias.example/api"})
	if err != nil {
		t.Fatal(err)
	}
	if err = guard("10.85.0.7"); !errors.Is(err, app.ErrBlocked) {
		t.Fatal("DNS alias escaped CIDR Target policy")
	}
	if err = guard("10.84.0.7"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE auth_user SET is_active=FALSE WHERE id=$1`, user); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Permit(ctx, user, []string{"http://127.0.0.1/"}); !errors.Is(err, app.ErrForbidden) {
		t.Fatal("inactive member allowed")
	}
}
