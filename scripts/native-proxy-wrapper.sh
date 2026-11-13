#!/bin/sh
set -eu

tool="${0##*/}"
tool_dir="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
use_proxy="${SKYSHIELD_FORCE_SYSTEM_PROXY:-legacy}"
if [ "$use_proxy" = legacy ]; then
  use_proxy=0
  if [ -n "${HTTP_PROXY:-}" ] && [ -z "${NO_PROXY:-}${no_proxy:-}" ]; then use_proxy=1; fi
fi
if [ "$use_proxy" != 1 ]; then
  exec "$tool_dir/$tool" "$@"
fi
: "${HTTP_PROXY:?enabled system proxy is missing}"
case "$tool" in
  subfinder|katana) exec "$tool_dir/$tool" -proxy "$HTTP_PROXY" "$@" ;;
  nuclei) exec "$tool_dir/$tool" -proxy-internal -proxy "$HTTP_PROXY" "$@" ;;
  observer-ward) exec "$tool_dir/$tool" --proxy "$HTTP_PROXY" "$@" ;;
  ffuf) exec "$tool_dir/$tool" -x "$HTTP_PROXY" "$@" ;;
  *) echo "unsupported local proxy wrapper" >&2; exit 1 ;;
esac
