#!/usr/bin/env bash
set -euo pipefail
umask 077

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
secret_dir="$project_dir/.local/secrets"
mkdir -p -m 700 "$secret_dir"

for name in db_password jwt_secret ai_encryption_key; do
  path="$secret_dir/$name"
  if [[ -L "$path" || ( -e "$path" && ! -f "$path" ) ]]; then
    printf '密钥路径不是普通文件：%s\n' "$path" >&2
    exit 1
  fi
  if [[ ! -e "$path" ]]; then
    openssl rand -base64 32 | tr -d '\n' > "$path"
    chmod 600 "$path"
  fi
done

printf '本地开发密钥已准备好：%s\n' "$secret_dir"
