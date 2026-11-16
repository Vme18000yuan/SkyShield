#!/usr/bin/env bash
set -euo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$project_dir"

if ! command -v docker >/dev/null || ! docker compose version >/dev/null 2>&1; then
  printf '需要安装 Docker Engine 和 Docker Compose 插件。\n' >&2
  exit 1
fi

compose=(docker compose -f "$project_dir/compose.tianqiong.yaml")
usage() {
  printf '用法：./docker.sh [start|pull|run|restart|stop|status|logs [服务名...]]\n'
}

# Daily operations use the containers from the existing deployment. Looking up
# Compose service IDs avoids replacing a Hub deployment with source-built images.
declare -A runtime_containers
load_runtime_containers() {
  local service ids state
  for service in postgres redis loki server frontend nginx agent migrate volume-preflight bootstrap cert-init; do
    ids="$("${compose[@]}" ps --all --quiet "$service")"
    if [[ -z "$ids" || "$ids" == *$'\n'* ]]; then
      printf '未找到唯一的 %s 容器；请先使用 ./docker.sh 或 ./docker.sh pull 完成部署。\n' "$service" >&2
      return 1
    fi
    runtime_containers["$service"]="$ids"
  done
  for service in migrate volume-preflight bootstrap cert-init; do
    state="$(docker inspect --format '{{.State.Status}} {{.State.ExitCode}}' "${runtime_containers[$service]}")"
    if [[ "$state" != 'exited 0' ]]; then
      printf '初始化容器 %s 未成功完成（%s）；请先检查部署日志。\n' "$service" "$state" >&2
      return 1
    fi
  done
  for service in postgres redis loki server frontend nginx agent; do
    state="$(docker inspect --format '{{.State.Status}}' "${runtime_containers[$service]}")"
    case "$state" in
      running|exited|created) ;;
      *) printf '容器 %s 状态为 %s，请先检查容器状态。\n' "$service" "$state" >&2; return 1 ;;
    esac
  done
}

wait_runtime_service() {
  local service="$1" state attempt
  for ((attempt=0; attempt<120; attempt++)); do
    state="$(docker inspect --format '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "${runtime_containers[$service]}")"
    case "$state" in
      'running healthy'|'running none') return 0 ;;
      *unhealthy*|exited*|dead*) printf '容器 %s 未能就绪（%s），请查看 ./docker.sh logs %s。\n' "$service" "$state" "$service" >&2; return 1 ;;
    esac
    sleep 1
  done
  printf '等待容器 %s 就绪超时，请查看 ./docker.sh logs %s。\n' "$service" "$service" >&2
  return 1
}

start_runtime_service() {
  local service="$1" state
  state="$(docker inspect --format '{{.State.Status}}' "${runtime_containers[$service]}")"
  if [[ "$state" != 'running' ]]; then
    docker start "${runtime_containers[$service]}" >/dev/null
  fi
}

start_existing_runtime() {
  local service published port
  for service in postgres redis loki; do start_runtime_service "$service"; done
  for service in postgres redis loki; do wait_runtime_service "$service"; done
  for service in server frontend nginx agent; do
    start_runtime_service "$service"
    wait_runtime_service "$service"
  done
  printf '现有 Docker 服务已启动，可使用 ./docker.sh status 查看状态。\n'
  if published="$("${compose[@]}" port nginx 443 2>/dev/null)" && [[ "$published" == 0.0.0.0:* ]]; then
    port="${published##*:}"
    printf '界面监听 0.0.0.0:%s；本机访问 https://localhost:%s，其他设备访问 https://<服务器IP>:%s。\n' "$port" "$port" "$port"
  fi
}

case "${1:-start}" in
  start|pull)
    action="${1:-start}"
    if [[ $# -gt 1 ]]; then usage >&2; exit 2; fi
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
  run|restart)
    if [[ $# -ne 1 ]]; then usage >&2; exit 2; fi
    load_runtime_containers
    if [[ "$1" == restart ]]; then
      for service in agent nginx frontend server loki redis postgres; do
        state="$(docker inspect --format '{{.State.Status}}' "${runtime_containers[$service]}")"
        if [[ "$state" == running ]]; then
          docker stop --time 30 "${runtime_containers[$service]}" >/dev/null
        fi
      done
    fi
    start_existing_runtime
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
    usage
    ;;
  *)
    printf '未知命令：%s\n' "$1" >&2
    usage >&2
    exit 2
    ;;
esac
