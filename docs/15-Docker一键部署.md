# Docker 一键部署

SkyShield 的 Docker 入口为项目根目录的 `./docker.sh`，它使用
`compose.tianqiong.yaml` 从本仓库构建 Server、Frontend 和 Bootstrap。
根目录的 `compose.yaml` 是上游 LunaFox 历史发布配置，不是此入口。

## 前置条件

- Linux 上可用的 Docker Engine、Docker Compose 插件和 OpenSSL。
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
更新镜像仍需执行 `./docker.sh pull`，更新本地构建仍需执行 `./docker.sh`。

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
