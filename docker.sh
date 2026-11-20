#!/usr/bin/env bash
set -euo pipefail

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$project_dir"

mkdir -p "$project_dir/.local"
if [[ "${SKYSHIELD_UPDATER_CHILD:-}" != 1 && "${1:-start}" != logs && "${1:-start}" != status && "${1:-start}" != network-check ]]; then
  exec 9>"$project_dir/.local/deployment.lock"
  flock -n 9 || { printf '部署或更新正在执行，请稍后重试。\n' >&2; exit 1; }
fi

if ! command -v docker >/dev/null || ! docker compose version >/dev/null 2>&1; then
  printf '需要安装 Docker Engine 和 Docker Compose 插件。\n' >&2
  exit 1
fi

compose=(docker compose -f "$project_dir/compose.tianqiong.yaml")
network_override="$project_dir/.local/network/compose.network.yaml"
network_adapter=false
if [[ -f "$network_override" && ! -L "$network_override" ]]; then
  compose+=(-f "$network_override")
  network_adapter=true
fi
release_override="$project_dir/.local/updater/compose.release.json"
if [[ -f "$release_override" && ! -L "$release_override" ]]; then compose+=(-f "$release_override"); fi
usage() {
  printf '用法：./docker.sh [start|pull|run|restart|stop|status|logs [服务名...]|network-check [内网域名或IP]]\n'
}

# Daily operations use the containers from the existing deployment. Looking up
# Compose service IDs avoids replacing a Hub deployment with source-built images.
declare -A runtime_containers
load_runtime_containers() {
  local service ids state
  local services=(postgres redis loki server frontend nginx agent migrate volume-preflight bootstrap cert-init)
  if [[ "$network_adapter" == true ]]; then services+=(network-helper); fi
  for service in "${services[@]}"; do
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

print_access_address() {
  local published port container
  if published="$("${compose[@]}" port nginx 443 2>/dev/null)" && [[ "$published" == 0.0.0.0:* ]]; then
    port="${published##*:}"
  else
    container="$("${compose[@]}" ps --quiet nginx)"
    port="$(docker inspect --format '{{ index .Config.Labels "org.skyshield.network.public-port" }}' "$container" 2>/dev/null || true)"
  fi
  if [[ "$port" =~ ^[0-9]+$ ]]; then
    printf '界面监听 0.0.0.0:%s；本机访问 https://localhost:%s，其他设备访问 https://<服务器IP>:%s。\n' "$port" "$port" "$port"
  fi
}

start_existing_runtime() {
  local service
  if [[ "$network_adapter" == true ]]; then
    start_runtime_service network-helper
    wait_runtime_service network-helper
  fi
  for service in postgres redis loki; do start_runtime_service "$service"; done
  for service in postgres redis loki; do wait_runtime_service "$service"; done
  for service in server frontend nginx agent; do
    start_runtime_service "$service"
    wait_runtime_service "$service"
  done
  printf '现有 Docker 服务已启动，可使用 ./docker.sh status 查看状态。\n'
  print_access_address
}

case "${1:-start}" in
  start|pull)
    action="${1:-start}"
    if [[ "$action" == start && -f "$release_override" ]]; then action=pull; fi
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
    if ! command -v python3 >/dev/null; then printf '部署检查和网络配置继承需要 Python 3，请先安装 python3。\n' >&2; exit 1; fi
    database_volume="$(docker compose -f "$project_dir/compose.tianqiong.yaml" config --format json | python3 -c 'import json,sys; print(json.load(sys.stdin)["volumes"]["postgres_data"]["name"])')"
    if docker volume inspect "$database_volume" >/dev/null 2>&1; then
      # Reinstalling a checkout must not generate new credentials against an
      # existing database. PostgreSQL only adopts its password on first init;
      # losing the AI key also makes persisted model credentials unreadable.
      for name in db_password jwt_secret ai_encryption_key; do
        path="$project_dir/.local/secrets/$name"
        if [[ ! -s "$path" ]]; then
          printf '数据库卷 %s 已存在，但密钥 .local/secrets/%s 缺失或为空。为避免凭据与旧数据不一致，已停止部署；请从备份恢复原 .local/secrets，参见 docs/15-Docker一键部署.md 的数据库认证故障恢复说明。\n' "$database_volume" "$name" >&2
          exit 1
        fi
      done
    fi
    bash "$project_dir/scripts/tianqiong-dev-init.sh"
    mkdir -p -m 700 "$project_dir/.local/updater/socket"
    network_args=(prepare --root "$project_dir")
    compose=(docker compose -f "$project_dir/compose.tianqiong.yaml")
    if [[ "$action" == "pull" ]]; then
      compose+=(-f "$project_dir/compose.hub.yaml")
      network_args+=(--hub)
    fi
    network_override="$(python3 "$project_dir/scripts/deployment-network.py" "${network_args[@]}")"
    if [[ -n "$network_override" ]]; then compose+=(-f "$network_override"); fi
    if [[ -f "$release_override" && ! -L "$release_override" ]]; then compose+=(-f "$release_override"); fi
    "${compose[@]}" config --quiet
    # Reject occupied host ports before downloading or building application images.
    python3 "$project_dir/scripts/deployment-network.py" preflight --root "$project_dir"
    if [[ "$action" == "pull" ]]; then
      "${compose[@]}" pull
      if [[ -n "$network_override" ]]; then
        helper_image="$("${compose[@]}" config --format json | python3 -c 'import json,sys; print(json.load(sys.stdin)["services"]["network-helper"]["image"])')"
        if ! docker run --rm --network none --pull never --entrypoint /usr/local/bin/skyshield-network-helper "$helper_image" --help >/dev/null 2>&1; then
          printf '当前 Bootstrap 镜像不包含网络适配器；请使用匹配本次源码的新版镜像标签，或显式关闭 SKYSHIELD_INHERIT_HOST_NETWORK。\n' >&2
          exit 1
        fi
      fi
      "${compose[@]}" up -d --no-build --pull never
    else
      "${compose[@]}" up -d --build
    fi
    python3 "$project_dir/scripts/deployment-network.py" stop-disabled --root "$project_dir"
    printf '服务已交由 Docker Compose 启动，可使用 ./docker.sh status 查看状态。\n'
    print_access_address
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
      if [[ "$network_adapter" == true ]]; then docker stop --time 30 "${runtime_containers[network-helper]}" >/dev/null; fi
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
  network-check)
    if [[ $# -gt 2 ]]; then usage >&2; exit 2; fi
    python3 "$project_dir/scripts/deployment-network.py" check --root "$project_dir" "${@:2}"
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
