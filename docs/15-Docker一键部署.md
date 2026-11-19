# Docker 一键部署

SkyShield 的 Docker 入口为项目根目录的 `./docker.sh`，它使用
`compose.tianqiong.yaml` 从本仓库构建 Server、Frontend 和 Bootstrap。
根目录的 `compose.yaml` 是上游 LunaFox 历史发布配置，不是此入口。

## 前置条件

- Linux 上可用的 Docker Engine、Docker Compose 插件、Python 3 和 OpenSSL。
- 能访问构建依赖的镜像仓库、Go 模块源和 npm 源。
- 使用独立 Docker daemon：上游 Agent 要求固定的 `lunafox_data`、
  `lunafox_engine_execution` 和 `lunafox_agent_state` 卷名。若同名卷
  已属于其他部署，预检会拒绝启动，不会删除或重新归属这些卷。
- Docker 配置引用上游分发的闭源 Agent 镜像，其使用条件见
  [闭源 Agent 说明](../NOTICE-CLOSED-ARTIFACTS.md)。

## 启动和停止

```bash
./docker.sh                 # 从本地源码构建并启动
./docker.sh pull            # 拉取 Docker Hub 镜像并启动
./docker-start.sh           # 启动已经部署的容器
./docker-restart.sh         # 重启已经部署的容器
./docker.sh status          # 查看容器状态
./docker.sh logs            # 查看日志；可在后面指定服务名
./docker.sh stop            # 停止容器并保留数据卷
```

日常启动和重启分别等价于 `./docker.sh run` 和 `./docker.sh restart`。
它们复用当前部署的容器、镜像与数据卷，不重新构建、拉取镜像或执行初始化容器；
需要先通过 `./docker.sh` 或 `./docker.sh pull` 完成部署。重启会中断正在执行的任务。
正式版本通过 `./update.sh --mode docker` 升级，[系统更新说明](16-系统更新.md)包含备份位置和恢复步骤。

默认在 `0.0.0.0:8443` 监听，本机可访问 `https://127.0.0.1:8443`，
其他设备可访问 `https://服务器IP:8443`；首次证书为自签名证书。
需要调整地址时，可复制 `.env.example` 为 `.env`，将 `PUBLIC_HOST` 设置为
客户端使用的域名或 IP，并按需修改 `PUBLIC_PORT`。`0.0.0.0` 是监听地址，
不应作为浏览器地址或 `PUBLIC_HOST`；请根据实际访问范围配置主机防火墙。
首次启动的默认管理员账号为 `admin`，初始密码
为 `admin`；请在首次登录后立即修改密码。

首次启动在 `.local/secrets` 中生成数据库密码、JWT 密钥和 AI 加密密钥，
并以文件方式提供给需要的服务。再次启动会复用已有文件，不会自动轮换或
删除它们；请安全备份，尤其是 AI 加密密钥，丢失后已有加密模型凭据无法
解密。本机 `.env`、运行数据、密钥与构建缓存不会进入 Docker 构建上下文
或 Git 仓库。模型 API 密钥在登录后的模型配置中设置。

`docker compose config` 可以验证配置，但不等同于完整容器部署验收；遇到
预检或构建失败时，应先检查 `./docker.sh logs` 和 Docker 环境，不要删除
其他项目的数据卷。

`pull` 使用 `compose.hub.yaml` 中的 `solder110/skyshield-*` 镜像，默认
使用 `latest`，可通过 `.env` 的 `SKYSHIELD_IMAGE_TAG` 指定已发布的
`sha-<提交号>` 标签。当前发布平台为 `linux/amd64`。扫描 Agent 和引擎
继续使用上游镜像；它们不包含在 `solder110/skyshield-*` 镜像中。

## 部署机网络继承

`./update.sh --mode docker` 将安装的镜像摘要保存至 `.local/updater/compose.release.json`，
日常部署继续使用已安装版本。

验证范围：已完成脚本回归、生成配置和隔离容器的 DNS/hosts、TCP、动态 Engine 创建验证；
官方 Agent 领取真实扫描任务、结果回传及完整一键部署仍待独立环境验证。
升级时须同时更新部署脚本与配套 Server、Bootstrap 镜像。旧版 Bootstrap 镜像不包含新增
网络适配器；使用旧镜像时，可设置 `SKYSHIELD_INHERIT_HOST_NETWORK=0` 沿用原部署行为。

默认 `SKYSHIELD_INHERIT_HOST_NETWORK=1`、`SKYSHIELD_NETWORK_MODE=host`。
应用组件、初始化组件、Agent 和动态扫描引擎共享运行 Docker 的 Linux 主机网络空间，
从而使用相同的网卡、路由和 VPN。PostgreSQL、Redis、Loki 继续留在项目网桥，数据库和
缓存没有新增宿主机发布端口；Nginx 直接监听 `PUBLIC_PORT`，HTTPS 浏览器入口仍为 `8443`。
Frontend `3000`、Server HTTP `8080` 和 gRPC `9090` 也会占用并监听宿主机端口，应按实际访问范围配置防火墙。
脚本在重建服务前检查端口冲突，不会停止占用端口的其他程序。

网络适配器使用同一版本的 SkyShield Bootstrap 镜像，从宿主网络读取 DNS 和 hosts。
活动的 systemd-resolved stub 优先于其上游列表，以保留 VPN 的 split DNS；不会自动补入
Google、Cloudflare 等公网 DNS。执行组件通过适配器解析内部服务名称，动态扫描容器通过
Agent 的项目专用 Docker API 连接取得相同的网络默认值；上游 Agent 镜像和二进制保持原样。
Docker daemon 配置、宿主机 DNS、路由和 hosts 文件均不会被改写。

内置 `resolvers.txt` 若仍为原始默认内容，会迁移为当前部署环境的 DNS；之后只更新本项目
生成且未被人工编辑的内容，并同步目录中的文件统计。用户修改过的列表及其他自定义
解析器资源保持原样。`SKYSHIELD_SYSTEM_DNS=0` 可在本地部署中关闭这项默认资源同步。

需要优先保持容器网桥隔离时，在 `.env` 设置 `SKYSHIELD_NETWORK_MODE=bridge`。
网桥模式同样传入 DNS、hosts 和代理，但能否访问内网还取决于宿主机是否允许网桥/NAT
流量进入 VPN。新建项目网桥会避开已知的本机和 VPN 路由；也可设置
`SKYSHIELD_DOCKER_SUBNET=172.30.240.0/24`。已有网络不会被自动删除，发现网段冲突时
会明确报告；处理冲突时应保留数据卷和密钥。

代理默认读取部署 shell 或 `.env` 中的 `HTTP_PROXY`、`HTTPS_PROXY`、`ALL_PROXY`、
`NO_PROXY`（含小写版本）。代理凭据只保存在 `.local/network/` 的私有配置中，传给工具的是
经过认证的本机中继地址；已有明确代理设置的工具配置优先保留。平台内显式配置的系统代理
继续遵守平台设置。使用 sudo 时，shell 的代理变量可能被 sudo 清除；可在 `.env` 配置，
或按 sudo 策略保留所需变量。改变 DNS、hosts 或代理配置后，重新执行 `./docker.sh pull`
（源码部署用 `./docker.sh`）同步；systemd-resolved 已有 stub 后的分域 DNS 变化由其自身处理。

诊断示例：

```bash
./docker.sh network-check
./docker.sh network-check intranet.example
./docker.sh network-check 10.20.30.40:443
./docker.sh network-check https://intranet.example/
```

指定端口或 URL 时还会比较部署机和 Server 的 TCP 连通性。DNS/TCP 成功不等于应用认证
或 TLS 证书验证通过。部署在虚拟机中时，虚拟机本身必须先能访问目标；Windows/macOS
物理电脑上的 VPN、PAC 和 DNS 不会自动透传到虚拟机。该适配器面向同机 Linux Docker
Engine，不适用于远程 Docker daemon；Docker Desktop 的宿主网络功能也有独立限制。

`SKYSHIELD_INHERIT_HOST_NETWORK=0` 可关闭项目网络适配；此后解析器列表由用户维护。
切换网络模式会重建执行组件并中断任务，应等待扫描和 AI 任务结束后执行。选择旧镜像标签
时须配套旧源码，或明确关闭新适配器；缺少适配器二进制的 Bootstrap 镜像会在预检时报错。
