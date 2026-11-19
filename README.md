# SkyShield

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)

天穹（SkyShield）把组织资产、传统扫描结果、AI 对话与任务、漏洞证据和通知记录汇集到同一平台。从组织定位到根域名后，可以查看关联资产，再追溯一条发现来自哪次运行、哪份证据，以及是否已经复核。项目基于 LunaFox，并集成 ARTEX 和 Norma 运行时。请仅对明确授权的目标使用扫描和测试功能。

## 部署后从哪里访问

| 部署方式 | 本机浏览器 | 同一局域网 | 默认监听 |
| --- | --- | --- | --- |
| 本地单机 | `http://127.0.0.1:3000` | `http://服务器IP:3000` | `0.0.0.0:3000` |
| Docker | `https://127.0.0.1:8443` | `https://服务器IP:8443` | `0.0.0.0:8443` |

**全新部署**的管理员账号和初始密码均为 `admin`。首次登录后请修改密码。升级或重启已有实例时，请使用该实例当前的账号密码。

## 常用功能

| 页面入口 | 可以做什么 |
| --- | --- |
| 资产 → 选择组织 | 查看根域名、子域名、IP 数量和资产明细。根域名详情提供层级树和可折叠拓扑，默认隐藏已识别的 CDN 节点，可按需显示。 |
| AI 对话 → 探索链路 | 按任务阅读一次运行，查看每段的目的、来源、实际步骤及登记的发现。 |
| AI 对话 / AI 任务 → 反连监控 | 为 SSRF 等授权测试分配独立 HTTP/HTTPS、可选 DNS 地址，查看回连证据；AI 可自动申请、等待和停止探针。 |
| 漏洞管理 → 漏洞详情 | 分别查看漏洞名称、类型、来源和复核状态。已保存的 HTTP 请求和响应可按 Raw 格式核对。 |
| AI 设置 → AI 通知推送 | 配置漏洞通知渠道，查看投递记录和失败原因。 |
| 工具 → 数据包重放 | 手动查看和发送 HTTP Raw 报文，核对返回的响应。 |
| 页面右上角 MCP → 客户端授权与连接诊断 | 为外部客户端选择组织和工具，签发、轮换或撤销密钥，验证实际地址、鉴权和工具发现。 |

### 怎样读懂探索链路

打开一段已有对话，点击 **探索链路**。主线卡片、任务选择器和步骤明细顶部都显示本段任务。明确的待办或公开任务声明直接作为标题，任务切换时另起一段。同一项任务可跨多页，步骤编号用于定位原始调用。

历史运行也支持这套视图。界面会用“根据本段记录归纳”标出从多个步骤归纳的主题，例如盘点资产与域名归属、比对数据记录或整理证据。点击“查看依据”可以回到对应活动。记录不足时显示“本段任务目的未记录”。标题生成使用已保存的数据，额外模型调用为零。

手动对话可以记录模型回合、工具批次和实际执行区间。时间有重叠的步骤会单独标识。已登记发现可展开查看回执和证据。仅有活动时间的历史记录按保存顺序展示。

### SSRF 等带外测试的反连监控

对话顶部和任务详情都有 **反连监控** 入口。AI 使用 `oast_create` 申请测试地址、`oast_poll` 增量查询证据；探针自动关联当前会话或任务，支持有效期和撤销，证据持久化保存。

默认复用站点的 HTTP/HTTPS 入口。远程目标测试前，配置目标能访问的 `OAST_PUBLIC_URL`；可选独立 HTTP 监听和带 NS 委派的 DNS 服务。Docker 环境变量已接入 `.env.example`，本地启动可直接传入同名环境变量。[配置、部署和验证说明](server/internal/oast/README.md)

## 本地一键部署

Linux 需安装 Go 1.26.10、Node.js 22、pnpm、Python 3、PostgreSQL 和 Redis。在项目根目录运行对应命令。

```bash
./local.sh start       # 首次部署或按当前源码重新构建
./local-start.sh       # 启动已部署的本地服务
./local-restart.sh     # 重启已部署的本地服务
./local.sh status      # 查看服务状态
./local.sh stop        # 停止本地服务
```

如 3000 端口被其他程序占用，首次部署可执行 `SKYSHIELD_LOCAL_FRONTEND_PORT=3300 ./local.sh start`。已有实例可执行 `SKYSHIELD_LOCAL_FRONTEND_PORT=3300 ./local-restart.sh` 切换端口。脚本会保存成功启动的端口。日常启动和重启复用已有构建。[本地部署与依赖说明](docs/08-本地单机部署.md)

## Docker 一键部署

Linux 部署机需安装 Docker Engine、Docker Compose 插件、Python 3 和 OpenSSL。在项目根目录运行对应命令。

```bash
./docker.sh                 # 按当前源码构建并启动
./docker.sh pull            # 拉取 Docker Hub 上的配套镜像并启动
./docker-start.sh           # 启动现有容器
./docker-restart.sh         # 重启现有容器
./docker.sh status          # 查看容器状态
./docker.sh logs            # 查看日志
./docker.sh stop            # 停止服务并保留数据
```

首次启动使用自签名 HTTPS 证书。如需调整访问地址和证书名称，在 `.env` 配置 `PUBLIC_HOST` 和 `PUBLIC_PORT`。重启会中断运行中的任务。[配置示例](.env.example)

Docker 镜像使用 `linux/amd64` 平台，四个应用镜像位于 Docker Hub 的 `solder110/skyshield-server`、`solder110/skyshield-frontend`、`solder110/skyshield-bootstrap` 和 `solder110/skyshield-nginx`。首次部署默认使用 `latest`。如需固定镜像标签，请通过 `.env` 的 `SKYSHIELD_IMAGE_TAG` 指定四个镜像均已发布的 `sha-<12 位提交号>`。

默认配置继承 Linux 部署机的路由、VPN、DNS、hosts 和代理。宿主网络模式还会使用 3000、8080、9090 端口，PostgreSQL、Redis 和 Loki 通过项目网桥通信。遇到内网域名解析差异时，可运行 `./docker.sh network-check 内网域名或IP` 对比部署机与 Server 的结果。需要网桥隔离时可在 `.env` 设置 `SKYSHIELD_NETWORK_MODE=bridge`。[Docker 网络与部署说明](docs/15-Docker一键部署.md)

首次启动在 `.local/secrets` 生成数据库、JWT 和 AI 加密密钥。请随业务数据备份，并保存在部署机上。Docker 部署引用上游分发的闭源 Agent 镜像，该组件适用上游授权条款。[闭源 Agent 说明](NOTICE-CLOSED-ARTIFACTS.md)

### 外部 MCP 客户端

MCP 与网页共用地址。默认本机入口为 `http://127.0.0.1:3000/mcp`，局域网可使用 `http://服务器IP:3000/mcp`；Docker 使用 `https://服务器域名或IP:8443/mcp`。如果部署时修改了端口或 `PUBLIC_HOST`，客户端地址须使用浏览器实际访问的地址再加 `/mcp`。

登录天穹后，打开右上角 **MCP → 客户端授权与连接诊断**，按下面步骤配置。

1. 新建一个客户端并填写便于识别的名称。
2. 勾选该客户端可以读取的组织。“全部组织”会额外包含本人创建的无组织对话。
3. 勾选需要开放的工具。漏洞复核会修改漏洞状态，只有额外勾选复核工具时才会开放。
4. 点击创建并生成密钥。密钥只显示这一次，请复制保存。
5. 把 MCP 地址和密钥填入外部客户端，然后回到同一窗口点击“测试连接”。提示握手和工具发现成功后，该客户端即可调用获准的工具。

下面的示例使用 Docker HTTPS 地址。请把 `https://skyshield.example.com/mcp` 换成你的实际 MCP 地址，并把 `<MCP密钥>` 换成界面刚生成的密钥。JSON 配置需确保占位符保持为字符串内容，不要保留尖括号。

**VS Code**

```json
{
  "servers": {
    "skyshield": {
      "type": "http",
      "url": "https://skyshield.example.com/mcp",
      "headers": {
        "Authorization": "Bearer <MCP密钥>"
      }
    }
  }
}
```

**Cursor**

```json
{
  "mcpServers": {
    "skyshield": {
      "type": "streamable-http",
      "url": "https://skyshield.example.com/mcp",
      "headers": {
        "Authorization": "Bearer <MCP密钥>"
      }
    }
  }
}
```

**Claude Code**

```json
{
  "mcpServers": {
    "skyshield": {
      "type": "http",
      "url": "https://skyshield.example.com/mcp",
      "headers": {
        "Authorization": "Bearer <MCP密钥>"
      }
    }
  }
}
```

**Codex**

```toml
[mcp_servers.skyshield]
url = "https://skyshield.example.com/mcp"
http_headers = { Authorization = "Bearer <MCP密钥>" }
```

轮换密钥会立即使旧密钥失效；撤销客户端也会立即关闭它的访问权限。AI 设置中的 MCP 页面用于管理“天穹连接外部 MCP 服务”，与这里的“外部客户端连接天穹”方向不同。[MCP 工具与权限说明](server/internal/mcp/README.md)

## 开发与测试

前端位于 `frontend/`，Go 后端位于 `server/`，扫描引擎位于 `extensions/engines/`。其中的工具、测试和 `resources/skills/` 脚本是构建、验证或产品功能所需的内部依赖；项目根目录提供部署及日常启动、重启入口。

```bash
(cd frontend && pnpm install --frozen-lockfile && pnpm test)
(cd server && go test ./...)
python3 scripts/test_server_image_tools.py solder110/skyshield-server:latest
```

真实模型密钥通过运行时配置提供，不应提交至仓库或写入构建参数。`.local/`、`.env`、本机数据库、日志和编译产物不属于源码。

## 系统更新

在部署主机的项目目录运行 `./update.sh` 安装 GitHub 最新正式发布。脚本会识别本地或 Docker 部署，
确认后备份数据库与配置、安装版本并验证服务。同一目录存在两种部署时，附加 `--mode local` 或
`--mode docker`。[系统更新说明](docs/16-系统更新.md)

## 来源与许可

SkyShield 采用 **[GNU AGPL-3.0](LICENSE)**（SPDX：`AGPL-3.0-only`）。该声明适用于 SkyShield 自有代码及修改，并保留各部分原有的授权条件。

LunaFox 衍生源码继续遵循 `GPL-3.0-only`，原许可证和声明分别保存在 [GPLv3 原文](LICENSES/GPL-3.0-only.txt)及 [LunaFox 原始声明](LICENSES/LunaFox-NOTICE.txt)。GPLv3 与 AGPLv3 的组合遵循各自第 13 条，包括适用于组合作品的网络源码提供要求。`third_party/artex` 使用 `AGPL-3.0-only`，`third_party/norma` 使用 `Apache-2.0`，`third_party/go-mitmproxy` 使用 `MIT`；其他依赖和素材保留各自许可。完整范围见 [NOTICE](NOTICE)。闭源 Agent 继续受[独立条款](NOTICE-CLOSED-ARTIFACTS.md)约束。原有上游发行清单仅用于记录来源，不代表 SkyShield 已发布相同镜像或版本。
