# SkyShield

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)

SkyShield is an attack surface management platform combining asset inventory, scan scheduling, vulnerability evidence, HTTP replay and AI agents. It builds on LunaFox and integrates ARTEX and Norma. Use scanning and testing features only on authorized targets.

## Local deployment

On Linux, install Go 1.26.10, Node.js 22, pnpm, Python 3, PostgreSQL and Redis, then run:

```bash
./local.sh start
```

The default URL is `http://127.0.0.1:3000`. Use `./local.sh status` and `./local.sh stop` to manage it. See the [local deployment guide](docs/08-本地单机部署.md) for details.

## Docker deployment

Install Docker Engine, the Compose plugin and OpenSSL, then run:

```bash
./docker.sh                 # build and start from local source
./docker.sh pull            # pull published Docker Hub images and start
```

The default URL is `https://127.0.0.1:8443` with a self-signed certificate. See [.env.example](.env.example) and the [Docker guide](docs/15-Docker一键部署.md). Use `./docker.sh status`, `./docker.sh logs`, and `./docker.sh stop` to manage the stack.

Published images use the `solder110/skyshield-server`, `skyshield-frontend`, `skyshield-bootstrap` and `skyshield-nginx` repositories. `latest` is the default; set `SKYSHIELD_IMAGE_TAG` to a published `sha-<commit>` tag to pin a version. The current release targets `linux/amd64`.

Database, JWT and AI encryption keys are generated once in `.local/secrets` and reused. Back up those keys; never commit them. Local data, secrets and build outputs are excluded from Git and Docker build contexts.

The Docker stack uses a separately distributed closed-source LunaFox Agent image. It is not included in this repository and is subject to [its own terms](NOTICE-CLOSED-ARTIFACTS.md). Verify its terms and use an isolated Docker environment before deployment.

## Source and licenses

SkyShield uses the **[GNU AGPL-3.0](LICENSE)** (`AGPL-3.0-only`). This grant covers SkyShield-authored code and modifications, subject to the retained terms for each part.

LunaFox-derived source retains its `GPL-3.0-only` terms; the original [GPLv3 text](LICENSES/GPL-3.0-only.txt) and [upstream notice](LICENSES/LunaFox-NOTICE.txt) are preserved. GPLv3/AGPLv3 combinations follow section 13 of both licenses, including the remote-network source-offer requirements applicable to the combination. `third_party/artex` is `AGPL-3.0-only`, `third_party/norma` is `Apache-2.0` and `third_party/go-mitmproxy` is `MIT`; other dependencies and assets retain their own licenses. See [NOTICE](NOTICE) for scope and the [separate closed Agent terms](NOTICE-CLOSED-ARTIFACTS.md). Historical upstream release manifests do not represent SkyShield releases.
