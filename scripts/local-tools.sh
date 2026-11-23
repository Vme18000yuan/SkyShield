#!/usr/bin/env bash
set -euo pipefail
umask 077

project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_dir="${SKYSHIELD_LOCAL_DATA_DIR:-$project_dir/.local/dev-runtime}"
tools_dir="$runtime_dir/tools/bin"
go_bin="${SKYSHIELD_GO:-$(command -v go || true)}"

if [[ ! -x "$go_bin" ]]; then
  printf '需要 Go 1.26.10；请设置 SKYSHIELD_GO 指向 go 可执行文件。\n' >&2
  exit 1
fi
for command in curl git make gcc tar unzip python3 sha256sum flock timeout; do
  if ! command -v "$command" >/dev/null; then printf '缺少本地构建依赖：%s\n' "$command" >&2; exit 1; fi
done
case "$(uname -m)" in
  x86_64) arch=amd64; webp_arch=x86-64; naabu_sum=13972348cfa00d3fb18f8ecd4b62a9c3b57bc75ef037cea87cd4bed3463dca02; ffuf_sum=86307885810d3c36ba4a3e9ba5178c2d9027bba0dd7f4ea39e39e7c972b62396; nuclei_sum=234c12cc5288af071abdcd6f854245b6067345556e1235cf96b76725c1004357; webp_sum=1c5ffab71efecefa0e3c23516c3a3a1dccb45cc310ae1095c6f14ae268e38067 ;;
  aarch64|arm64) arch=arm64; webp_arch=aarch64; naabu_sum=a6d67c1e2a0ced4347512dd3265458b50b207908b17c9c76d38a755f5536c58b; ffuf_sum=89ad4f50345e6a9a48ecc8d241811d582cedf96279276b48d666b12b31260484; nuclei_sum=d1ed1a5c0df49d8fcd64cab4ff5840b793d6bf133b082bb6a7f67d5fc0f9c327; webp_sum=69f5eebe203e0f3942fe37986209a1725741be19c152950a4283b376c95ec798 ;;
  *) printf '不支持的本地 CPU 架构：%s\n' "$(uname -m)" >&2; exit 1 ;;
esac
mkdir -p -m 700 "$runtime_dir" "$tools_dir"
exec 8>"$runtime_dir/tools/.install.lock"
if ! flock -n 8; then printf '本地扫描工具安装正在进行。\n' >&2; exit 1; fi
tmp_dir="$(mktemp -d "$runtime_dir/.tools-build.XXXXXX")"
trap 'rm -rf -- "$tmp_dir"' EXIT
export GOTOOLCHAIN=local
export GODEBUG="${GODEBUG:+$GODEBUG,}http2client=0"
export PATH="$(dirname "$go_bin"):$PATH"
export GOBIN="$tools_dir"

install_go_tool() {
  local name="$1" module="$2"
  if [[ ! -x "$tools_dir/$name" ]]; then
    printf '构建本地工具 %s\n' "$name"
    timeout 600 "$go_bin" install "$module"
  fi
}

download_checked() {
  local url="$1" checksum="$2" destination="$3"
  curl --fail --location --retry 3 --connect-timeout 20 --max-time 300 --output "$destination" "$url"
  printf '%s  %s\n' "$checksum" "$destination" | sha256sum --check --status
}

install_go_tool subfinder github.com/projectdiscovery/subfinder/v2/cmd/subfinder@v2.12.0
install_go_tool puredns github.com/d3mondev/puredns/v2@v2.1.2-0.20260223162428-46bd4c963ec2
install_go_tool katana github.com/projectdiscovery/katana/cmd/katana@v1.6.1

# Keep the AI information-gathering DNS tool aligned with the Server image.
if [[ ! -x "$tools_dir/dnsx" ]]; then
  printf '安装本地 DNSX（信息收集 DNS 解析）\n'
  case "$arch" in
    amd64) dnsx_sum=dabd54146d63b53fdd08186156ca117fcb79656c66153e3d3c61367aa87a9943 ;;
    arm64) dnsx_sum=072c615f7c2acd3d056cbc6a8da105eaffdc78cbc5c3aa549b7fd0e3dc1436e8 ;;
  esac
  archive="dnsx_1.2.2_linux_${arch}.zip"
  download_checked "https://github.com/projectdiscovery/dnsx/releases/download/v1.2.2/$archive" "$dnsx_sum" "$tmp_dir/$archive"
  unzip -q "$tmp_dir/$archive" dnsx LICENSE.md -d "$tmp_dir/dnsx"
  mkdir -p "$runtime_dir/tools/licenses"
  install -m 644 "$tmp_dir/dnsx/LICENSE.md" "$runtime_dir/tools/licenses/dnsx.txt"
  chmod 755 "$tmp_dir/dnsx/dnsx"
  mv "$tmp_dir/dnsx/dnsx" "$tools_dir/dnsx"
fi

if [[ ! -x "$tools_dir/httpx" ]]; then
  printf '构建本地 HTTPX（包含截图请求头修正）\n'
  module_dir="$("$go_bin" mod download -json github.com/projectdiscovery/httpx@v1.10.0 | python3 -c 'import json,sys; print(json.load(sys.stdin)["Dir"])')"
  cp -a "$module_dir" "$tmp_dir/httpx"
  chmod -R u+w "$tmp_dir/httpx"
  (cd "$tmp_dir/httpx" && git apply --check "$project_dir/extensions/engines/screenshot/patches/httpx-v1.10.0-browser-headers.patch" && git apply "$project_dir/extensions/engines/screenshot/patches/httpx-v1.10.0-browser-headers.patch" && "$go_bin" build -trimpath -o "$tools_dir/httpx" ./cmd/httpx)
fi
if [[ ! -x "$tools_dir/website_discovery/httpx" ]]; then
  printf '构建网站探测使用的 HTTPX 版本\n'
  mkdir -p "$tools_dir/website_discovery"
  GOBIN="$tools_dir/website_discovery" timeout 600 "$go_bin" install github.com/projectdiscovery/httpx/cmd/httpx@v1.8.1
fi

if [[ ! -x "$tools_dir/massdns" ]]; then
  printf '构建本地 MassDNS\n'
  git clone --quiet --depth 1 --branch v1.1.0 https://github.com/blechschmidt/massdns.git "$tmp_dir/massdns"
  make -C "$tmp_dir/massdns" -s
  install -m 755 "$tmp_dir/massdns/bin/massdns" "$tools_dir/massdns"
fi

if [[ ! -x "$tools_dir/naabu" ]]; then
  archive="naabu_2.4.0_linux_${arch}.zip"
  download_checked "https://github.com/projectdiscovery/naabu/releases/download/v2.4.0/$archive" "$naabu_sum" "$tmp_dir/$archive"
  unzip -q "$tmp_dir/$archive" naabu -d "$tmp_dir/naabu"
  install -m 755 "$tmp_dir/naabu/naabu" "$tools_dir/naabu"
fi
if [[ ! -x "$tools_dir/ffuf" ]]; then
  archive="ffuf_2.2.1_linux_${arch}.tar.gz"
  download_checked "https://github.com/ffuf/ffuf/releases/download/v2.2.1/$archive" "$ffuf_sum" "$tmp_dir/$archive"
  mkdir -p "$tmp_dir/ffuf"
  tar -xzf "$tmp_dir/$archive" -C "$tmp_dir/ffuf" ffuf
  install -m 755 "$tmp_dir/ffuf/ffuf" "$tools_dir/ffuf"
fi
if [[ ! -x "$tools_dir/nuclei" ]]; then
  archive="nuclei_3.4.10_linux_${arch}.zip"
  download_checked "https://github.com/projectdiscovery/nuclei/releases/download/v3.4.10/$archive" "$nuclei_sum" "$tmp_dir/$archive"
  unzip -q "$tmp_dir/$archive" nuclei -d "$tmp_dir/nuclei"
  install -m 755 "$tmp_dir/nuclei/nuclei" "$tools_dir/nuclei"
fi
if [[ ! -x "$tools_dir/cwebp" ]]; then
  archive="libwebp-1.6.0-linux-${webp_arch}.tar.gz"
  download_checked "https://storage.googleapis.com/downloads.webmproject.org/releases/webp/$archive" "$webp_sum" "$tmp_dir/$archive"
  tar -xzf "$tmp_dir/$archive" -C "$tmp_dir"
  install -m 755 "$tmp_dir/libwebp-1.6.0-linux-${webp_arch}/bin/cwebp" "$tools_dir/cwebp"
fi

if [[ ! -x "$tools_dir/waymore" || ! -x "$tools_dir/uro" ]]; then
  printf '安装本地 URL 收集工具\n'
  if command -v uv >/dev/null; then
    uv venv --allow-existing --python "$(command -v python3)" "$runtime_dir/tools/python"
    uv pip install --python "$runtime_dir/tools/python/bin/python" -r "$project_dir/extensions/engines/url_collection/requirements.lock"
  else
    python3 -m venv "$runtime_dir/tools/python"
    "$runtime_dir/tools/python/bin/python" -m pip install -r "$project_dir/extensions/engines/url_collection/requirements.lock"
  fi
  ln -sfn "$runtime_dir/tools/python/bin/uro" "$tools_dir/uro"
  cat > "$tools_dir/waymore" <<EOF
#!/bin/sh
exec "$runtime_dir/tools/python/bin/python" -m waymore.waymore "\$@"
EOF
  chmod 755 "$tools_dir/waymore"
fi

if [[ ! -x "$tools_dir/observer-ward" ]]; then
  export RUSTUP_HOME="$runtime_dir/tools/rustup"
  export CARGO_HOME="$runtime_dir/tools/cargo"
  if [[ ! -x "$CARGO_HOME/bin/cargo" ]]; then
    printf '在本地目录安装 Rust 构建工具链\n'
    case "$arch" in
      amd64) rust_target=x86_64-unknown-linux-gnu ;;
      arm64) rust_target=aarch64-unknown-linux-gnu ;;
    esac
    rust_url="https://static.rust-lang.org/rustup/archive/1.28.2/${rust_target}/rustup-init"
    curl --fail --location --retry 3 --connect-timeout 20 --output "$tmp_dir/rustup-init" "$rust_url"
    curl --fail --location --retry 3 --connect-timeout 20 --output "$tmp_dir/rustup-init.sha256" "$rust_url.sha256"
    (cd "$tmp_dir" && sha256sum --check --status rustup-init.sha256)
    chmod 755 "$tmp_dir/rustup-init"
    "$tmp_dir/rustup-init" -y --no-modify-path --profile minimal --default-toolchain 1.94.0
  fi
  export PATH="$CARGO_HOME/bin:$PATH"
  if ! RUSTUP_TOOLCHAIN=1.94.0 cargo --version >/dev/null 2>&1; then
    rustup toolchain install 1.94.0 --profile minimal
  fi
  printf '构建本地指纹工具 Observer Ward\n'
  git clone --quiet --depth 1 --branch v2026.6.28-lunafox.1 https://github.com/yyhuni/observer_ward_for_luna.git "$tmp_dir/observer-ward"
  [[ "$(git -C "$tmp_dir/observer-ward" rev-parse HEAD)" == 65801cf6d4b3dd4bb07ea7a1cf6e849713c42ea6 ]]
  (cd "$tmp_dir/observer-ward" && RUSTUP_TOOLCHAIN=1.94.0 cargo build --locked --release --package observer_ward)
  install -m 755 "$tmp_dir/observer-ward/target/release/observer_ward" "$tools_dir/observer-ward"
fi

if ! command -v google-chrome >/dev/null && ! command -v chromium >/dev/null; then
  printf '截图引擎需要本机安装 Chromium 或 Google Chrome。\n' >&2
  exit 1
fi
for binding in subdomain_discovery/subfinder url_collection/katana nuclei_vulnerability/nuclei fingerprint_detection/observer-ward directory_scan/ffuf; do
  destination="$tools_dir/$binding"
  mkdir -p "$(dirname "$destination")"
  if [[ -e "$destination" ]] && ! cmp -s "$project_dir/scripts/native-proxy-wrapper.sh" "$destination"; then
    printf '本地代理包装路径已被其他文件占用：%s\n' "$destination" >&2
    exit 1
  fi
  if [[ ! -e "$destination" ]]; then install -m 755 "$project_dir/scripts/native-proxy-wrapper.sh" "$destination"; fi
done
website_httpx="$tools_dir/website_discovery/httpx"
website_httpx_real="$tools_dir/website_discovery/httpx.real"
if [[ ! -e "$website_httpx_real" ]]; then
  if cmp -s "$project_dir/scripts/native-httpx-proxy-wrapper.sh" "$website_httpx"; then
    printf '网站探测 HTTPX 实际程序缺失：%s\n' "$website_httpx_real" >&2
    exit 1
  fi
  cp -p "$website_httpx" "$website_httpx_real.new"
  mv "$website_httpx_real.new" "$website_httpx_real"
fi
if ! cmp -s "$project_dir/scripts/native-httpx-proxy-wrapper.sh" "$website_httpx"; then
  if ! cmp -s "$website_httpx" "$website_httpx_real"; then
    printf '网站探测 HTTPX 路径已被其他文件占用：%s\n' "$website_httpx" >&2
    exit 1
  fi
  install -m 755 "$project_dir/scripts/native-httpx-proxy-wrapper.sh" "$website_httpx.new"
  mv -f "$website_httpx.new" "$website_httpx"
fi
printf '本地扫描工具已准备：%s\n' "$tools_dir"
