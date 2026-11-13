# system module

- ServerLocationScheduler 只使用单个可取消 one-shot timer 和单个 pending/in-flight self lookup；当前成功在精确 7 天边界刷新，失败在 5 分钟与共享 provider cooldown 的较晚边界重试。

- `GET /v1/admin/system/logEntries` 固定查询 Server source，不接受 caller 覆盖容器、文件、selector 或任意 LogQL。
- `pageSize` 未传时为 200，必须为 `1..500`。前端的 `100/200/500/1000/2000/5000` 仅表示浏览器 latest-N 窗口；Server 对每个请求立即返回最多 500 行，且不保存跨请求的 viewer 累积结果。
- 响应中的 `nextPageToken`、`previousPageToken`、`hasOlder`、`hasNewer`、`caughtUp`、`gap` 和 `gapReason` 是 opaque pagination/follow 合约。Loki 内部上限无法证明同纳秒 cursor 连续性时，必须返回 gap 或 retryable 结果，不得把 cursor 前移并声称已经追平。
- `server_location_snapshot` 只保存当前单 Server 由 FreeIPAPI 观测到的最后成功公网出口位置；`repository` 原子替换完整 singleton，`application.ServerLocationReader` 只投影 `current`/`expired` 成功状态，无成功行即 unknown。

- `GET/PUT/DELETE /v1/admin/system/httpProxy/current` 管理 installation-wide HTTP/HTTPS 出站代理，仅 active superuser 可操作。代理 URL（包括可选认证信息）使用 AI credential SecretBox 加密保存；API 只返回移除 userinfo 的代理地址。已安装 `http.DefaultTransport` 的动态 Proxy policy 覆盖 Server 的标准 `net/http` 客户端、默认 transport 的 OCI 请求，以及 Server 管理的 Git 源同步。代理启用后，后续外部请求立即切换；本地回环、`server`、`nginx`、`loki` 组件通信直连。Agent 内扫描引擎的目标 HTTP 流量由独立 Agent 运行时处理，不由 Server 进程的代理传输控制。
