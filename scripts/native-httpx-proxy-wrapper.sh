#!/bin/sh
set -eu

tool_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
use_proxy="${SKYSHIELD_FORCE_SYSTEM_PROXY:-legacy}"
if [ "$use_proxy" = legacy ]; then
  use_proxy=0
  if [ -n "${HTTP_PROXY:-}" ] && [ -z "${NO_PROXY:-}${no_proxy:-}" ]; then use_proxy=1; fi
fi
if [ "$use_proxy" = 1 ]; then
  : "${HTTP_PROXY:?enabled system proxy is missing}"
  exec "$tool_dir/httpx.real" -http-proxy "$HTTP_PROXY" "$@"
fi
exec "$tool_dir/httpx.real" "$@"
