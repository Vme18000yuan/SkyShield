#!/usr/bin/env bash
set -euo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go_cmd="${TIANQIONG_GO:-go}"
test_pattern="${TIANQIONG_TEST_PATTERN:-TestInitialSchemaBootstrapsEmptyPostgres|TestFrozenMigrationBaselineUsesNumberedPairsAndManifestChecksums}"
pg_bin="${TIANQIONG_POSTGRES_BIN:-}"
if [[ -z "$pg_bin" ]]; then
  for version in 18 17 16 15 14; do
    if [[ -x "/usr/lib/postgresql/$version/bin/initdb" ]]; then
      pg_bin="/usr/lib/postgresql/$version/bin"
      break
    fi
  done
fi
[[ -x "$pg_bin/initdb" ]] || { echo '未找到 PostgreSQL initdb；设置 TIANQIONG_POSTGRES_BIN。' >&2; exit 1; }
command -v "$go_cmd" >/dev/null || { echo '未找到 Go；设置 TIANQIONG_GO。' >&2; exit 1; }

task_pg_dir="$(mktemp -d /tmp/tianqiong-contract-pg-XXXXXX)"
task_pg_port="$(python3 - <<'PY'
import socket
with socket.socket() as s:
    s.bind(('127.0.0.1',0))
    print(s.getsockname()[1])
PY
)"
cleanup() {
  "$pg_bin/pg_ctl" -D "$task_pg_dir/data" -m fast -w stop >/dev/null 2>&1 || true
  if [[ "$task_pg_dir" == /tmp/tianqiong-contract-pg-* ]]; then rm -rf -- "$task_pg_dir"; fi
}
trap cleanup EXIT

"$pg_bin/initdb" -D "$task_pg_dir/data" -U tianqiong_test --auth=trust --no-locale --encoding=UTF8 > "$task_pg_dir/init.log"
"$pg_bin/pg_ctl" -D "$task_pg_dir/data" -l "$task_pg_dir/server.log" -o "-h 127.0.0.1 -p $task_pg_port -k $task_pg_dir" -w start >/dev/null
"$pg_bin/createdb" -h 127.0.0.1 -p "$task_pg_port" -U tianqiong_test lunafox_migration_test_tianqiong

cd "$project_dir/server"
LUNAFOX_EMPTY_POSTGRES_MIGRATION_DSN="postgres://tianqiong_test@127.0.0.1:$task_pg_port/lunafox_migration_test_tianqiong?sslmode=disable" \
GOMAXPROCS="${GOMAXPROCS:-4}" "$go_cmd" test -p 4 -count=1 ./cmd/server \
  -run "$test_pattern"
