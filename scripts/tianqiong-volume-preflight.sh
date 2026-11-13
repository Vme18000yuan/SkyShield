#!/usr/bin/env bash
set -euo pipefail

# The closed LunaFox Agent expects these exact daemon-wide volume names.
# Refuse to mount an existing deployment's volumes into Tianqiong.
for volume in lunafox_data lunafox_engine_execution lunafox_agent_state; do
  owner="$(docker volume inspect "$volume" --format '{{index .Labels "com.docker.compose.project"}}' 2>/dev/null || true)"
  if [[ "$owner" != "tianqiong" ]]; then
    printf '卷 %s 不属于天穹项目（当前归属：%s）；请使用独立的 Docker daemon。\n' "$volume" "${owner:-未标记}" >&2
    exit 1
  fi
done
