# 内置反连监控（OAST）

用于 AI 对话与 AI 任务中的 SSRF、XXE 等带外验证。HTTP/HTTPS 回连和可选的权威 DNS 回连共用探针；数据库保存探针、过期时间及证据，重启不丢失。该服务不主动请求被测目标，也不执行回连内容。

## 使用

- 对话顶部 **反连监控**、任务详情 **反连监控** 标签可创建探针、复制地址、检查请求和停止接收。
- AI 内置工具：`oast_create`、`oast_list`、`oast_poll`、`oast_revoke`。自动绑定运行时提供的会话/任务，模型不能指定其他会话、用户或任务。
- 默认授权给 `auto`、`pentest`、`mainagent`、`planner`、`worker`、`retester`；`reporter` 默认只有查询工具。自定义 Agent 可在工具管理中配置。
- 可向 AI 描述：“为头像导入接口的 url 参数创建独立反连探针，在授权测试后等待回连，并给出触发请求与回连记录的对应关系。”
- 每个测试点使用独立探针。不要点击、预览或用 curl 自测正式测试地址，以免污染证据。需要验证部署时单独创建名为“部署自测”的探针。
- `oast_poll` 接受 `probe_id`、`after`、`wait_seconds`（0–20 秒），返回 `nextAfter` 和 `hasMore`。继续查询时传回游标；`hasMore=true` 时先读下一页。无回连不等于无漏洞，DNS 命中不等于 HTTP 请求已执行。

## 配置

| 环境变量 | 默认值 / 用途 |
| --- | --- |
| `OAST_PUBLIC_URL` | 默认使用 `PUBLIC_URL`；必须是目标能访问的 HTTP(S) origin，例如 `https://callback.example.com`，不可带路径或凭据 |
| `OAST_HTTP_LISTEN` | 空；可选独立 HTTP 监听，如 `0.0.0.0:8081`，仅提供回连接收 |
| `OAST_DNS_LISTEN` | 空；可选 DNS 监听，如 `0.0.0.0:5353`，同时监听 UDP 和 TCP |
| `OAST_DNS_ZONE` | 空；独立委派区域，如 `cb.example.com` |
| `OAST_DNS_ANSWER_IP` | 空；该区域 A 或 AAAA 回答使用的 IP |

HTTP 默认复用 Server 的 `/oast/<随机令牌>` 路径；已接入 Nginx 和本地 Next.js 代理。独立监听端口不提供管理 API。HTTPS 在反向代理终止 TLS；应使用目标信任的证书。回连地址不会从用户提交的 Host / Forwarded 头推断。

本地部署可直接传入环境变量，`scripts/local-deploy.py` 会保留这些变量。例如将 `OAST_PUBLIC_URL` 设置为目标可达的服务器地址，并设置 `OAST_HTTP_LISTEN=0.0.0.0:8081`。仅有 `127.0.0.1` 的地址不能被远程目标访问。公网可达性需要从实际被测网络验证，配置成功不代表已经可达。

`./docker.sh` 使用的 `compose.tianqiong.yaml` 已透传上述五个变量，可在项目根目录 `.env` 设置。默认宿主网络模式下，监听直接绑定宿主接口；DNS 应绑定空闲的具体接口 IP:53，避免与本机 DNS / 项目网络助手的 53 端口冲突。仅使用现有 HTTPS 入口时无需额外端口。

网桥模式可使用 [可选部署覆盖文件](../../../deploy/compose.oast.example.yaml)，与现有 compose 文件合并后启动，为独立 HTTP + DNS 映射端口。例如先设置 `SKYSHIELD_NETWORK_MODE=bridge`，生成网络配置后执行 `docker compose -f compose.tianqiong.yaml -f .local/network/compose.network.yaml -f deploy/compose.oast.example.yaml up -d server`。维护该部署时需一直保留此覆盖文件；常规 `docker.sh` 不会自动加载自定义覆盖文件。宿主网络模式不使用此端口映射示例。

### DNS 委派

1. 预留独立区域，例如 `cb.example.com`；在父区域配置 `cb.example.com NS ns1.cb.example.com`。
2. 为 `ns1.cb.example.com` 配置指向服务器公网 IP 的 A/AAAA glue（具体入口取决于 DNS 服务商）。
3. 将公网 UDP **和** TCP 53 转发到 `OAST_DNS_LISTEN`；设置 `OAST_DNS_ZONE=cb.example.com` 与 `OAST_DNS_ANSWER_IP`。
4. 使用独立自测探针，对返回的 `dnsName` 发起查询，确认页面出现 DNS 记录。普通通配符 A 记录不能代替 NS 委派。

DNS 服务只回答自己的区域，不提供递归。来源 IP 通常是递归解析器；DNS 证据与 HTTP 证据分开记录。生成的 `dnsName` 用于 DNS 测试，HTTP 测试使用单独返回的 `httpUrl`。

## API 与数据边界

管理接口沿用 JWT 与原对话/任务的可见性检查：

```
GET    /v1/aiConversations/:conversation/oastProbes?before=...
POST   /v1/aiConversations/:conversation/oastProbes
GET    /v1/aiConversations/:conversation/oastProbes/:probe/events?after=...&waitSeconds=0
DELETE /v1/aiConversations/:conversation/oastProbes/:probe
```

任务接口将 `aiConversations/:conversation` 替换为 `aiTasks/:task`。创建体为 `{ "label": "测试点", "ttlSeconds": 3600, "requestId": "UUID" }`，重复请求 ID 与同一参数返回原探针；参数变化返回 409。列表按每页 50 条分页，`nextBefore` 表示下一页。事件按 ID 升序增量查询。DELETE 撤销回连能力，保留已有证据。

回连路径是公开的只写能力：160 位随机令牌无法用于读取记录。HTTP 方法、路径后缀和查询参数均可采集；请求体保留最多 8 KiB 的原始字节（Base64），请求头最多 8 KiB，路径最多 2 KiB。常见凭据头脱敏，超限显式标记。正文与 URL 仍可能包含敏感信息，仅所属用户能查看。

来源字段使用直连 peer；不信任客户端伪造的 X-Forwarded-For。内置 Nginx 覆盖转发头，可在证据中检查代理观察到的来源。回连内容始终作为不可信文本显示，AI 工具说明也明确禁止将其当作指令。

每个探针最多 200 条记录，每个用户最多 1000 个未撤销且未过期的探针、5000 个保留探针。默认有效期 1 小时，允许 60 秒至 24 小时。结束 AI 运行不撤销探针，以便接收延迟回连；到期、撤销或所属任务不可见时拒绝继续接收。每小时分批清理过期超过 7 天的探针及其事件。记录容量以外的回连不继续入库，界面显示记录已满。

接收端共用速率限制（每秒 50 次、突发 100）与 16 个并发处理名额；独立监听具备读写超时和头大小限制。数据库事务原子限制事件数，并在分配事件 ID 前锁住探针，保证同一探针的增量游标不会漏掉并发提交。

## 升级与验证

新增 forward migration `000085_oast_monitor`，不改历史迁移。先运行匹配的 Server 迁移，再使用新前端。生产不要执行 down；它仅用于隔离测试。

```
cd server
go test ./internal/oast ./internal/bootstrap ./internal/modules/aisettings/application ./internal/config ./internal/middleware
go test ./cmd/server -run TestFrozenMigrationBaselineUsesNumberedPairsAndManifestChecksums
OAST_TEST_POSTGRES_DSN='host=127.0.0.1 user=postgres dbname=postgres sslmode=disable' go test -race ./internal/oast
```

PostgreSQL 测试在指定实例内创建随机命名的临时数据库，完成后仅删除该临时数据库；需要建库权限。测试覆盖 HTTP/DNS 实际收包、原始字节、凭据脱敏、跨用户/会话/任务访问、重试幂等、任务及独立主对话的运行租约、重启后的证据、延迟回连、并发上限、分页、取消和到期清理。
