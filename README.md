# SkyShield

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)

天穹（SkyShield）是一个攻击面管理平台，将资产归集、扫描、漏洞管理与 AI Agent 测试整合到同一界面。项目基于 LunaFox，并集成 ARTEX 和 Norma 运行时。

主要功能包括资产与根域名管理、扫描调度、漏洞证据查看、HTTP 数据包重放、AI 对话和自主任务。请仅对明确授权的目标使用扫描和测试功能。

## 本地一键部署

Linux 环境安装 Go 1.26.10、Node.js 22、pnpm、Python 3、PostgreSQL 和 Redis 后，在项目根目录执行：

```bash
./local.sh start       # 首次部署或重新构建
./local-start.sh       # 启动已部署的本地服务
./local-restart.sh     # 重启已部署的本地服务
```

启动后：

- 默认监听地址：`0.0.0.0:3000`
- 本机浏览器访问：`http://127.0.0.1:3000`
- 其他设备访问：`http://服务器IP:3000`
- 全新部署的管理员账号：`admin`，初始密码：`admin`

首次登录后请修改初始密码；已有数据库会保留修改后的密码。日常启动、重启复用现有构建，不重新编译或安装工具。使用 `./local.sh status` 查看状态，使用 `./local.sh stop` 停止。详细依赖、端口和参数见 [本地单机部署](docs/08-本地单机部署.md)。

## Docker 一键部署

在 Linux 部署机上安装 Docker Engine、Docker Compose 插件、Python 3 和 OpenSSL 后执行：

```bash
./docker.sh                 # 从本地源码构建并启动
./docker.sh pull            # 拉取 Docker Hub 镜像并启动
./docker-start.sh           # 启动现有容器
./docker-restart.sh         # 重启现有容器
./docker.sh network-check 内网域名或IP  # 比较部署机与 Server 的解析；支持 IP:端口/HTTP(S) URL
```

启动后：

- 默认监听地址：`0.0.0.0:8443`
- 本机浏览器访问：`https://127.0.0.1:8443`
- 其他设备访问：`https://服务器IP:8443`
- 全新部署的管理员账号：`admin`，初始密码：`admin`

首次登录后请修改初始密码；已有数据卷会保留修改后的密码。首次启动使用自签名证书。可通过 `.env` 配置用于生成链接和证书的 `PUBLIC_HOST` 以及访问端口 `PUBLIC_PORT`，参考 [.env.example](.env.example)。日常启动、重启复用已有容器，不构建或拉取镜像；重启会中断正在执行的任务。使用 `./docker.sh status` 查看状态，`./docker.sh logs` 查看日志，`./docker.sh stop` 停止服务并保留数据。

默认启用部署机网络继承：应用组件、Agent 和动态扫描引擎使用 Linux 宿主网络，沿用部署机的路由/VPN；DNS 优先使用活动的 systemd-resolved，保留分域解析，并读取部署机 hosts 和 HTTP/HTTPS/ALL_PROXY/NO_PROXY 配置。PostgreSQL、Redis 和 Loki 仍使用项目网桥，HTTPS 浏览器入口仍为 `8443`。宿主执行模式需要 `3000`、`8080`、`9090` 和 HTTPS 入口端口未被其他程序占用；可用 `.env` 中的 `SKYSHIELD_NETWORK_MODE=bridge` 选择网桥隔离，此时 VPN/防火墙须允许容器网桥转发。这里的宿主机指运行 Docker 的 Linux 主机或虚拟机；物理电脑的 VPN 不会自动成为虚拟机的网络。具体配置和诊断见 [Docker 部署说明](docs/15-Docker一键部署.md)。

发布镜像位于 Docker Hub 的 `solder110/skyshield-server`、`skyshield-frontend`、`skyshield-bootstrap` 和 `skyshield-nginx`，默认使用 `latest`；可在 `.env` 设置 `SKYSHIELD_IMAGE_TAG` 为指定的 `sha-<提交号>`。当前发布的镜像平台为 `linux/amd64`。

Server 镜像包含 AI 工作进程使用的 Python 及信息收集依赖、Node.js/npm/npx、Chromium、Puppeteer、Playwright CLI 和默认浏览器 MCP，以及 subfinder、dnsx、httpx、katana、nuclei、ffuf、nmap、masscan、ripgrep 等工具。浏览器 MCP 仍需在平台设置中启用；其他 MCP 服务的依赖需按各自要求配置。Nuclei 官方模板库不随镜像打包，需在运行时下载或使用 `-t` 指定可访问的模板；masscan 的实际运行还取决于容器网络权限。受管信息收集仍遵循原有代理与目标范围限制。

首次启动会在 `.local/secrets` 中生成数据库、JWT 和 AI 加密密钥；再次启动时复用已有密钥。请备份这些密钥，并勿将其提交到 Git。镜像构建不使用本机密钥或数据库。更多说明见 [Docker 部署说明](docs/15-Docker一键部署.md)。

Docker 配置使用上游公开分发的闭源 LunaFox Agent 镜像；该组件并非本仓库的开源源码，使用条件见 [闭源 Agent 说明](NOTICE-CLOSED-ARTIFACTS.md)。本仓库不包含该 Agent 的二进制或签名凭据。部署前请确认使用条件，并在独立 Docker 环境中验证；现有同名数据卷不会被脚本删除。

## 开发与测试

前端位于 `frontend/`，Go 后端位于 `server/`，扫描引擎位于 `extensions/engines/`。其中的工具、测试和 `resources/skills/` 脚本是构建、验证或产品功能所需的内部依赖；项目根目录提供部署及日常启动、重启入口。

```bash
(cd frontend && pnpm install --frozen-lockfile && pnpm test)
(cd server && go test ./...)
python3 scripts/test_server_image_tools.py solder110/skyshield-server:latest
```

真实模型密钥通过运行时配置提供，不应提交至仓库或写入构建参数。`.local/`、`.env`、本机数据库、日志和编译产物不属于源码。

## 系统更新

系统更新入口位于“关于天穹”。本地与 Docker 部署均可通过宿主机独立更新进程检查稳定版本，
由管理员确认后备份数据库与配置、安装版本并显示进度。首次接入、发布流程和失败恢复见
[系统更新说明](docs/16-系统更新.md)。

## 来源与许可

SkyShield 采用 **[GNU AGPL-3.0](LICENSE)**（SPDX：`AGPL-3.0-only`）。该声明适用于 SkyShield 自有代码及修改，并保留各部分原有的授权条件。

LunaFox 衍生源码继续遵循 `GPL-3.0-only`，原许可证和声明分别保存在 [GPLv3 原文](LICENSES/GPL-3.0-only.txt)及 [LunaFox 原始声明](LICENSES/LunaFox-NOTICE.txt)。GPLv3 与 AGPLv3 的组合遵循各自第 13 条，包括适用于组合作品的网络源码提供要求。`third_party/artex` 使用 `AGPL-3.0-only`，`third_party/norma` 使用 `Apache-2.0`，`third_party/go-mitmproxy` 使用 `MIT`；其他依赖和素材保留各自许可。完整范围见 [NOTICE](NOTICE)。闭源 Agent 继续受[独立条款](NOTICE-CLOSED-ARTIFACTS.md)约束。原有上游发行清单仅用于记录来源，不代表 SkyShield 已发布相同镜像或版本。
