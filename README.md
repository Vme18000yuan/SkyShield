# SkyShield

天穹（SkyShield）是一个攻击面管理平台，将资产归集、扫描、漏洞管理与 AI Agent 测试整合到同一界面。项目基于 LunaFox，并集成 ARTEX 和 Norma 运行时。

主要功能包括资产与根域名管理、扫描调度、漏洞证据查看、HTTP 数据包重放、AI 对话和自主任务。请仅对明确授权的目标使用扫描和测试功能。

## 本地一键部署

Linux 环境安装 Go 1.26.10、Node.js 22、pnpm、Python 3、PostgreSQL 和 Redis 后，在项目根目录执行：

```bash
./local.sh start
```

默认访问 `http://127.0.0.1:3000`。使用 `./local.sh status` 查看状态，使用 `./local.sh stop` 停止。详细依赖、端口和参数见 [本地单机部署](docs/08-本地单机部署.md)。

## Docker 一键部署

安装 Docker Engine、Docker Compose 插件和 OpenSSL 后执行：

```bash
./docker.sh                 # 从本地源码构建并启动
./docker.sh pull            # 拉取 Docker Hub 镜像并启动
```

默认通过 `https://127.0.0.1:8443` 访问，首次启动使用自签名证书。可通过 `.env` 配置 `PUBLIC_HOST` 和 `PUBLIC_PORT`，参考 [.env.example](.env.example)。使用 `./docker.sh status` 查看状态，`./docker.sh logs` 查看日志，`./docker.sh stop` 停止服务并保留数据。

发布镜像位于 Docker Hub 的 `solder110/skyshield-server`、`skyshield-frontend`、`skyshield-bootstrap` 和 `skyshield-nginx`，默认使用 `latest`；可在 `.env` 设置 `SKYSHIELD_IMAGE_TAG` 为指定的 `sha-<提交号>`。当前发布的镜像平台为 `linux/amd64`。

Server 镜像包含 AI 工作进程使用的 Python 及信息收集依赖、Node.js/npm/npx、Chromium、Puppeteer、Playwright CLI 和默认浏览器 MCP，以及 subfinder、dnsx、httpx、katana、nuclei、ffuf、nmap、masscan、ripgrep 等工具。浏览器 MCP 仍需在平台设置中启用；其他 MCP 服务的依赖需按各自要求配置。Nuclei 官方模板库不随镜像打包，需在运行时下载或使用 `-t` 指定可访问的模板；masscan 的实际运行还取决于容器网络权限。受管信息收集仍遵循原有代理与目标范围限制。

首次启动会在 `.local/secrets` 中生成数据库、JWT 和 AI 加密密钥；再次启动时复用已有密钥。请备份这些密钥，并勿将其提交到 Git。镜像构建不使用本机密钥或数据库。默认管理员账号及首次登录说明见 [部署说明](docs/15-Docker一键部署.md)。

Docker 配置使用上游公开分发的闭源 LunaFox Agent 镜像；该组件并非本仓库的开源源码，使用条件见 [闭源 Agent 说明](NOTICE-CLOSED-ARTIFACTS.md)。本仓库不包含该 Agent 的二进制或签名凭据。部署前请确认使用条件，并在独立 Docker 环境中验证；现有同名数据卷不会被脚本删除。

## 开发与测试

前端位于 `frontend/`，Go 后端位于 `server/`，扫描引擎位于 `extensions/engines/`。其中的工具、测试和 `resources/skills/` 脚本是构建、验证或产品功能所需的内部依赖；项目根目录只提供上述两种部署入口。

```bash
(cd frontend && pnpm install --frozen-lockfile && pnpm test)
(cd server && go test ./...)
python3 scripts/test_server_image_tools.py solder110/skyshield-server:latest
```

真实模型密钥通过运行时配置提供，不应提交至仓库或写入构建参数。`.local/`、`.env`、本机数据库、日志和编译产物不属于源码。

## 来源与许可

项目保留上游版权及许可：LunaFox 的相应源码使用 GPL-3.0-only，`third_party/artex` 使用 AGPL-3.0-only，`third_party/norma` 使用 Apache-2.0，`third_party/go-mitmproxy` 使用 MIT。原版权和适用范围见 [NOTICE](NOTICE)，各部分的许可证和修改说明见各目录及 [LICENSE](LICENSE)。原有上游发行清单仅用于记录来源，不代表 SkyShield 已发布相同镜像或版本。
