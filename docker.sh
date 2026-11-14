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
  start|pull)
    action="${1:-start}"
    if [[ $# -gt 1 ]]; then printf '用法：./docker.sh [start|pull|stop|status|logs [服务名...]]\n' >&2; exit 2; fi
    if ! command -v openssl >/dev/null; then printf '首次启动需要 openssl 生成本地密钥。\n' >&2; exit 1; fi
    # The upstream Agent uses fixed volume names. Fail before creating secrets,
    # images or containers if an existing installation owns one of them.
    for volume in lunafox_data lunafox_engine_execution lunafox_agent_state; do
      if docker volume inspect "$volume" >/dev/null 2>&1; then
        owner="$(docker volume inspect "$volume" --format '{{index .Labels "com.docker.compose.project"}}')"
        if [[ "$owner" != "tianqiong" ]]; then
          printf '卷 %s 已存在且不属于天穹项目，请使用独立 Docker daemon；未启动服务。\n' "$volume" >&2
          exit 1
        fi
      fi
    done
    bash "$project_dir/scripts/tianqiong-dev-init.sh"
    if [[ "$action" == "pull" ]]; then
      compose+=(-f "$project_dir/compose.hub.yaml")
    fi
    "${compose[@]}" config --quiet
    if [[ "$action" == "pull" ]]; then
      "${compose[@]}" pull
      "${compose[@]}" up -d --no-build --pull never
    else
      "${compose[@]}" up -d --build
    fi
    printf '服务已交由 Docker Compose 启动，可使用 ./docker.sh status 查看状态。\n'
    if published="$("${compose[@]}" port nginx 443 2>/dev/null)" && [[ "$published" == 0.0.0.0:* ]]; then
      port="${published##*:}"
      printf '界面监听 0.0.0.0:%s；本机访问 https://localhost:%s，其他设备访问 https://<服务器IP>:%s。\n' "$port" "$port" "$port"
    fi
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
    printf '用法：./docker.sh [start|pull|stop|status|logs [服务名...]]\n'
    ;;
  *)
    printf '未知命令：%s\n用法：./docker.sh [start|pull|stop|status|logs [服务名...]]\n' "$1" >&2
    exit 2
    ;;
esac
