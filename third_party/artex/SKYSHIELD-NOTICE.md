# ARTEX 运行时来源说明

来源：当前工作区 ARTEX，提交 `e740334b9042d6da64156b0adb37a293117e140e`。保留根目录 AGPL-3.0 许可证及原版权声明。

导入范围包括 Agent、远程 MCP 客户端及其编译、行为测试所需的配置、DB 类型、审批、记录、通知、流量等 Go 包。完整迁移工作另外复用了 ARTEX 的前端页面和类型契约，放在 SkyShield 对应组件及 `frontend/types/artex-ai.types.ts`，通过当前产品 API、认证和权限访问数据；没有挂载独立的 ARTEX Web 服务。

天穹通过自己的应用服务和编号迁移管理业务数据，不调用 ARTEX 的 `db.Open`，也不创建一套可独立编辑的 ARTEX 资产主库。保留 DB 包是现阶段上游 Agent 类型依赖的兼容边界，任务迁移将继续拆解这些具体 Store 依赖。

当前改动：

- `agent/chat.go`：增加每实例基础工具、WebFetch 开关和 Prompt 注入，保持默认上游调用行为；天穹显式注入平台工具。
- `agent/provider.go`：连通测试不注入宿主默认工具；日志保留连接状态，避免记录原始模型响应体；宿主可注入动态 HTTP 代理策略。
- `agent/assembly.go`、`agent/host_policy.go` 及 Goals/Planner/Worker/MainAgent/Chat：支持按运行上下文注入宿主工具策略、WebFetch 开关、提示词与收尾配置，不修改进程级全局工具注册；未传策略时沿用上游行为。
- `intercept/review_context.go` 和 `intercept/trace.go`：提供宿主读取显式审查背景与工具调用审计快照的接口，脚本快照由受限进程采集。
- `sidequestion`：允许以宿主的 UUID 会话身份关联侧问，并对任务 MainAgent 的会话分段使用独立旁路上下文；旧的第 0 段键保持兼容。
- `agent/assembly.go`：支持宿主标记延迟工具、为最终工具集注入运行范围内的拦截器及 Skill 解锁后的 MCP 装配；`agent/goals.go` 将目标拆解接入相同的宿主工具、Skill 和 MCP 装配。
- `agent/goals.go`：宿主使用统一资产库时，根据最终可用工具集决定是否附加上游范围提取指引；未提供宿主工具时保留原行为。
- `db/asset_dsl.go`：公开原版 DSL 的参数化编译入口，供宿主统一资产投影复用解析器；不连接独立的 ARTEX 资产库。
- `agent/intelligence.go`、`agent/host_policy.go`：情报开关和记录回调支持按运行上下文注入；未提供宿主策略时保留上游全局回调行为。
- `agent/host_policy.go` 及漏洞工作流：自动关联开关和写入回调按宿主运行实例隔离；`evidence.Store.Guard` 在事务持锁后复核宿主运行租约及开关，未配置时沿用上游行为。
- `agent/planner.go`：为宿主持久化的主 Agent 新增意图事件提供对应的规划提示；`agent/worker.go` 在宿主提供录制工具时注入原版流量提示，不向模型暴露代理凭据。
- `agent/tools.go`：意图创建时通过运行上下文调用宿主的资产范围检查，使用天穹统一资产库及任务租约；目标证明关系或状态写入失败时返回错误。无宿主资产策略时沿用原版检查行为。
- `traffic/traffic.go`：增加宿主的本机认证监听器和动态上游代理接入；多进程录制使用高熵 ID 和原子正文文件写入，并在关闭时停止代理。记录内容依旧可能包含原始凭据。
- `llmrec/capture.go` 和 `agent/provider.go`：宿主可为原始模型 HTTP 正文录制设置大小上限；未启用录制时不再复制请求正文，上游默认无上限行为保持兼容。
- `mcphttp/client.go`：远程客户端禁用自动重定向、限制 HTTP JSON 响应大小、禁止旧式 SSE 切换来源、限制关闭等待，并避免向上层返回服务端错误响应体。天穹适配层同时支持 Streamable HTTP、旧式 SSE 和隔离的 stdio MCP；本地 MCP 的凭据通过一次性管道传递，不放进启动参数。

`server/internal/modules/aiworkbench/application/calendar_window.go` 和对应的窗口回归测试改编自上游日历计划代码；`000014` 中的探索图结构也源自该提交，并调整为引用天穹统一资产身份。

`server/internal/modules/aiworkbench/repository/task_intent_hard_delete.go` 的独占后代筛选和 Token 汇总改编自上游意图删除流程，并适配统一资产和跨任务引用。

`server/internal/modules/aiworkbench/repository/task_coverage_graph.go` 的资产层级连线改编自上游覆盖图构建流程，并适配统一资产及组织归属。

`server/internal/modules/aiworkbench/application/credential_traffic.go` 的凭据流量匹配改编自上游实现，只读取所属任务的录制数据并加密保存匹配片段。

`server/internal/airuntime/llm_usage.go` 的逐次模型用量计量改编自上游 `llmrec`，只保存用量元数据，并适配账号、任务、会话和模型池隔离。

没有修改原工作区 ARTEX 源码。

AI 可靠性整改另外增加：模型调用时限和共享请求预算；可调整的上下文压缩窗口；显式处理
会话恢复错误；终态原因及有界文本预览。天穹通过本地固定的 Norma 副本保存工具执行意图和结果，
并使检查点失败终止运行。后台图压缩跟随任务取消，不继续消耗已停止任务的模型预算。
