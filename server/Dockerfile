# syntax=docker/dockerfile:1.7

# ============================================
# Go Server - build
# ============================================
FROM --platform=$BUILDPLATFORM golang:1.26.10 AS builder

ARG TARGETOS=linux
ARG TARGETARCH
ARG GO111MODULE=on
ARG GOPROXY=https://proxy.golang.org,direct

ENV GO111MODULE=$GO111MODULE \
    GOPROXY=$GOPROXY \
    CGO_ENABLED=0 \
    GOOS=$TARGETOS \
    GOARCH=$TARGETARCH

WORKDIR /src

# Cache module dependencies. The Server module uses the committed local
# contracts and engine-go replacements directly, so release builds must not
# depend on an untracked developer go.work file.
COPY contracts/go.mod contracts/go.sum ./contracts/
COPY engine-go/go.mod engine-go/go.sum ./engine-go/
COPY third_party/artex/go.mod third_party/artex/go.sum ./third_party/artex/
COPY third_party/norma/go.mod third_party/norma/go.sum ./third_party/norma/
COPY third_party/go-mitmproxy/go.mod third_party/go-mitmproxy/go.sum ./third_party/go-mitmproxy/
COPY server/go.mod server/go.sum ./server/
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    cd server && go mod download

# Copy only Server and contract sources required by the release binary.
COPY contracts ./contracts
COPY engine-go ./engine-go
COPY third_party/artex ./third_party/artex
COPY third_party/norma ./third_party/norma
COPY third_party/go-mitmproxy ./third_party/go-mitmproxy
COPY server ./server
COPY extensions/workflows ./extensions/workflows

# Build (static where possible)
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    cd server && go build -trimpath -o /out/server ./cmd/server

# Official, version-pinned CLI releases. Checksums are recorded here so a
# changed or corrupted upstream archive cannot silently enter the image.
FROM --platform=$BUILDPLATFORM alpine:3.23.2 AS ai-tools
ARG TARGETARCH
RUN apk add --no-cache ca-certificates unzip
RUN set -eu; \
    mkdir -p /out/bin /out/licenses; \
    fetch_zip() { \
      name="$1"; version="$2"; checksum="$3"; \
      archive="${name}_${version}_linux_${TARGETARCH}.zip"; \
      wget -q -T 60 -t 5 -O "/tmp/$archive" "https://github.com/projectdiscovery/${name}/releases/download/v${version}/${archive}"; \
      printf '%s  %s\n' "$checksum" "/tmp/$archive" | sha256sum -c -; \
      unzip -q "/tmp/$archive" "$name" -d /out/bin; \
      unzip -p "/tmp/$archive" LICENSE.md > "/out/licenses/$name.txt"; \
      test -s "/out/licenses/$name.txt"; \
      chmod 0755 "/out/bin/$name"; rm "/tmp/$archive"; \
    }; \
    case "$TARGETARCH" in \
      amd64) \
        subfinder_sha=a17651dfa2ea95605fb7c1bab1ff1363bf4d05987d45466b08f736494128938b; \
        dnsx_sha=dabd54146d63b53fdd08186156ca117fcb79656c66153e3d3c61367aa87a9943; \
        httpx_sha=63eac4dcd6e5c9867c94765fdaaf66e7b4eeae3474a1f06e600e266a1c81a53e; \
        katana_sha=503754f1bd370c3ef287df6998e317baed2dd75bdd13ea64034f09b80ca393f3; \
        nuclei_sha=234c12cc5288af071abdcd6f854245b6067345556e1235cf96b76725c1004357; \
        ffuf_sha=86307885810d3c36ba4a3e9ba5178c2d9027bba0dd7f4ea39e39e7c972b62396 ;; \
      arm64) \
        subfinder_sha=bb1578a5c83f27e4b5d2aeb4deb2ed9669fa062f84f102a4d65875908d4ca9bd; \
        dnsx_sha=072c615f7c2acd3d056cbc6a8da105eaffdc78cbc5c3aa549b7fd0e3dc1436e8; \
        httpx_sha=7f3eaf5a6839504f5e2ce29c3ce94aa2ddeeaa7619d9a497e017232c6dc49b1c; \
        katana_sha=663b4ed2c6559a7dacc819e816889940d567ff554ac3f543c8c8f9b50245cd45; \
        nuclei_sha=d1ed1a5c0df49d8fcd64cab4ff5840b793d6bf133b082bb6a7f67d5fc0f9c327; \
        ffuf_sha=89ad4f50345e6a9a48ecc8d241811d582cedf96279276b48d666b12b31260484 ;; \
      *) echo "Unsupported AI tools architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    fetch_zip subfinder 2.12.0 "$subfinder_sha"; \
    fetch_zip dnsx 1.2.2 "$dnsx_sha"; \
    fetch_zip httpx 1.10.0 "$httpx_sha"; \
    fetch_zip katana 1.6.1 "$katana_sha"; \
    fetch_zip nuclei 3.4.10 "$nuclei_sha"; \
    archive="ffuf_2.2.1_linux_${TARGETARCH}.tar.gz"; \
    wget -q -T 60 -t 5 -O "/tmp/$archive" "https://github.com/ffuf/ffuf/releases/download/v2.2.1/$archive"; \
    printf '%s  %s\n' "$ffuf_sha" "/tmp/$archive" | sha256sum -c -; \
    tar -xzf "/tmp/$archive" -C /out/bin ffuf; \
    tar -xOf "/tmp/$archive" LICENSE > /out/licenses/ffuf.txt; \
    test -s /out/licenses/ffuf.txt; chmod 0755 /out/bin/ffuf; rm "/tmp/$archive"

# Node is needed by stdio MCP servers configured to use node/npx. Keeping it in
# /usr/local also makes it available to the existing AI worker sandbox.
FROM --platform=$TARGETPLATFORM node:24.14.0-bookworm-slim AS ai-node
WORKDIR /opt/skyshield-ai-node
COPY server/ai-node/package.json server/ai-node/package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts --no-audit --no-fund

# ============================================
# Go Server - runtime
# ============================================
FROM debian:bookworm-20260112-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    chromium \
    curl \
    dnsutils \
    ffmpeg \
    git \
    jq \
    masscan \
    nmap \
    openssl \
    python3 \
    python3-pip \
    ripgrep \
    whois \
    && rm -rf /var/lib/apt/lists/* \
    && rm -f /etc/chromium.d/apikeys

COPY server/requirements-ai-tools.lock /tmp/requirements-ai-tools.lock
RUN python3 -m pip install --break-system-packages --no-cache-dir --require-hashes \
      -r /tmp/requirements-ai-tools.lock \
    && rm /tmp/requirements-ai-tools.lock

WORKDIR /app

COPY --from=ai-tools /out/bin/ /usr/local/bin/
COPY --from=ai-tools /out/licenses/ /usr/share/doc/skyshield-ai-tools/
COPY --from=ai-node /usr/local/bin/node /usr/local/bin/node
COPY --from=ai-node /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/npm
COPY --from=ai-node /opt/skyshield-ai-node/node_modules /usr/local/lib/skyshield-ai-node/node_modules
COPY server/ai-node/playwright-cli.js /usr/local/bin/playwright-cli
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx \
    && ln -s ../lib/skyshield-ai-node/node_modules/@playwright/mcp/cli.js /usr/local/bin/playwright-mcp \
    && ln -s /usr/lib/chromium/chromium /usr/local/bin/chromium \
    && chmod 0755 /usr/local/bin/playwright-cli
COPY --from=builder /out/server /usr/local/bin/server
COPY --from=builder /src/extensions/workflows /usr/local/share/lunafox/workflows
COPY resources/skills /usr/local/share/lunafox/skills
# The reviewed migration policy is read from disk at runtime, so the image has to
# carry it at the Server default MIGRATION_POLICY_PATH the upgrade eligibility
# check reads. The SQL migrations stay embedded in the binary.
COPY --from=builder /src/server/cmd/server/migrations/policy.json /usr/local/share/lunafox/migrations/policy.json

# Compose resolves the stable service name; this is the image-local operational
# command used by `docker compose exec server resetadmin`.
RUN ln -s /usr/local/bin/server /usr/local/bin/resetadmin

EXPOSE 8080

CMD ["server"]
