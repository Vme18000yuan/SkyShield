#!/usr/bin/env bash
set -euo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$project_dir"

if ! command -v docker >/dev/null || ! docker compose version >/dev/null 2>&1; then
  printf '需要安装 Docker Engine 和 Docker Compose 插件。\n' >&2
  exit 1
fi

compose=(docker compose -f "$project_dir/compose.tianqiong.yaml")
case "${1:-start}" in
  start)
    if [[ $# -gt 1 ]]; then printf '用法：./docker.sh [start|stop|status|logs [服务名...]]\n' >&2; exit 2; fi
    if ! command -v openssl >/dev/null; then printf '首次启动需要 openssl 生成本地密钥。\n' >&2; exit 1; fi
    bash "$project_dir/scripts/tianqiong-dev-init.sh"
    "${compose[@]}" config --quiet
    "${compose[@]}" up -d --build
    printf '服务已交由 Docker Compose 启动，可使用 ./docker.sh status 查看状态。\n'
    ;;
  stop)
    if [[ $# -ne 1 ]]; then printf '用法：./docker.sh stop\n' >&2; exit 2; fi
    "${compose[@]}" stop
    ;;
  status)
    if [[ $# -ne 1 ]]; then printf '用法：./docker.sh status\n' >&2; exit 2; fi
    "${compose[@]}" ps
    ;;
  logs)
    shift
    "${compose[@]}" logs --follow --tail=200 "$@"
    ;;
  -h|--help|help)
    printf '用法：./docker.sh [start|stop|status|logs [服务名...]]\n'
    ;;
  *)
    printf '未知命令：%s\n用法：./docker.sh [start|stop|status|logs [服务名...]]\n' "$1" >&2
    exit 2
    ;;
esac
