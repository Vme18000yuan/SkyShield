# 内置反连监控（OAST）

用于 AI 对话与 AI 任务中的 SSRF、XXE 等带外验证。HTTP/HTTPS 回连和可选的权威 DNS 回连共用探针；数据库保存探针、过期时间及证据，重启不丢失。该服务不主动请求被测目标，也不执行回连内容。

## 使用

- 对话顶部 **反连监控**、任务详情 **反连监控** 标签可创建探针、复制地址、检查请求和停止接收。
- AI 内置工具：`oast_status`、`oast_create`、`oast_list`、`oast_poll`、`oast_revoke`。自动绑定运行时提供的会话/任务，模型不能指定其他会话、用户或任务。
- 默认授权给 `auto`、`pentest`、`mainagent`、`planner`、`worker`、`retester`；`reporter` 默认只有查询工具。自定义 Agent 可在工具管理中配置。
- 可向 AI 描述：“为头像导入接口的 url 参数创建独立反连探针，在授权测试后等待回连，并给出触发请求与回连记录的对应关系。”
- 每个测试点使用独立探针。不要点击、预览或用 curl 自测正式测试地址，以免污染证据。需要验证部署时单独创建名为“部署自测”的探针。
- `oast_poll` 接受 `probe_id`、`after`、`wait_seconds`（0–20 秒），返回 `nextAfter` 和 `hasMore`。继续查询时传回游标；`hasMore=true` 时先读下一页。无回连不等于无漏洞，DNS 命中不等于 HTTP 请求已执行。

### AI 自主使用

反连监控面板用于查看证据和手动干预，打开面板不是 AI 使用反连的前提。
模型收到的工具说明包含完整的按需使用流程：判断当前授权测试是否需要带外证据，
检查配置，为测试点创建独立探针，提交触发请求，按观察窗口查询，结束时关闭探针。
普通问答或被动分析无需创建探针。

`oast_status` 不创建探针、不访问网络，只返回已配置的入口、协议、地址范围与限制。
`enabled=true` 表示配置存在，`reachability=not_verified` 表示尚未验证目标侧的实际
连通性。回环、私有、通配监听地址有明确说明；例如 `127.0.0.1` 不能供其他主机的
目标回连。AI 不应将配置问题或观测窗口内无回连解释为没有漏洞。

创建探针即开始后台接收，模型可以继续其他工作，并适时 `oast_poll`，不必持续占用
模型轮询。正常结束时先 `oast_revoke` 停止接收，再用最后的游标 `oast_poll` 读取
剩余记录，避免漏掉关闭前的最后回连。只关闭当前测试的探针，不能关闭其他 Worker
正在使用的探针。异步测试可以保留到明确的 `expiresAt`，需说明原因、探针 ID 和
截止时间；模型异常退出或人工中断时，有效期负责兜底停止接收。

运行时指引通过原生工具的 `Prompt` 注入模型实际看到的工具描述，因此已保存的旧
工具说明也能获得流程更新，无需覆盖管理员的 Agent 提示词或工具描述。管理员的
禁用、角色绑定、延迟工具加载及审批策略仍然生效。自定义 Agent 需获得相应工具绑定。
工具是否调用、观察多久、何时关闭由当前模型按测试场景决定，不硬编码为每次对话
自动开启，也不会为单个测试关闭平台的 HTTP/DNS 监听服务。

## 配置

| 环境变量 | 默认值 / 用途 |
| --- | --- |
| `OAST_PUBLIC_URL` | 默认从 `PUBLIC_URL` 取主机名，使用独立监听端口生成 HTTP 地址（默认 `http://主机:8899`）；可显式指定目标可达的 HTTP(S) origin，不可带路径或凭据 |
| `OAST_HTTP_LISTEN` | 默认 `0.0.0.0:8899`，仅提供回连接收；`off` 关闭独立监听 |
| `OAST_DNS_LISTEN` | 空；可选 DNS 监听，如 `0.0.0.0:5353`，同时监听 UDP 和 TCP |
| `OAST_DNS_ZONE` | 空；独立委派区域，如 `cb.example.com` |
| `OAST_DNS_ANSWER_IP` | 空；该区域 A 或 AAAA 回答使用的 IP |

HTTP 默认使用独立的 `8899/oast/<随机令牌>` 入口，不经过前端 3000 端口，也不提供管理 API。显式配置的 `OAST_PUBLIC_URL` 优先，使用 TLS 反向代理时须填写 HTTPS 地址；独立监听本身是 HTTP。Web 的旧 `/oast/` 路由继续支持已有探针，已有探针地址不会自动改写；新探针使用新的配置。设置 `OAST_HTTP_LISTEN=off` 且不指定反连 URL 时可复用原 Web 入口；Docker 网桥部署还需移除不再使用的反连端口映射。回连地址不会从用户提交的 Host / Forwarded 头推断。

本地部署可直接传入环境变量，`scripts/local-deploy.py` 会保留这些变量。例如将 `OAST_PUBLIC_URL` 设置为目标可达的服务器地址，并设置 `OAST_HTTP_LISTEN=0.0.0.0:8899`。仅有 `127.0.0.1` 的地址不能被远程目标访问。公网可达性需要从实际被测网络验证，配置成功不代表已经可达。

`./docker.sh` 使用的 `compose.tianqiong.yaml` 已透传上述五个变量，可在项目根目录 `.env` 设置。默认宿主网络模式下，监听直接绑定宿主接口；网桥模式已默认映射 HTTP 8899；DNS 应绑定空闲的具体接口 IP:53，避免与本机 DNS / 项目网络助手的 53 端口冲突。仅使用现有 HTTPS 入口时设置 `OAST_HTTP_LISTEN=off`，无需额外反连端口。

网桥模式可使用 [可选部署覆盖文件](../../../deploy/compose.oast.example.yaml)，与现有 compose 文件合并后启动，为可选 DNS 增加端口映射（HTTP 默认 8899）。例如先设置 `SKYSHIELD_NETWORK_MODE=bridge`，生成网络配置后执行 `docker compose -f compose.tianqiong.yaml -f .local/network/compose.network.yaml -f deploy/compose.oast.example.yaml up -d server`。维护该部署时需一直保留此覆盖文件；常规 `docker.sh` 不会自动加载自定义覆盖文件。宿主网络模式不使用此端口映射示例。

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

自主工具流程使用确定性模型 fixture，经过真实工具目录和执行器，验证普通对话、
Worker 任务上下文及任务主对话的 status → list → create → poll → revoke → 最终 poll。
测试还检查旧描述装饰后指引仍可见、禁用/角色授权仍生效、关闭前迟到的证据被读到，
以及其他探针不受影响。这是工具链验证，不代表对外部目标的真实漏洞测试。
