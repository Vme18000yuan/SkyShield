# FOFA 网络空间搜索

入口：**工具 → 网络空间搜索**（`/tools/space-search/`）。协议按 [FOFA 官方 API](https://fofa.info/api) 与[官方 SDK](https://fofapro.github.io/fofa-py/_modules/fofa/client.html)接入，生产请求固定使用 `https://fofa.info`。

## 使用

1. 管理员打开“FOFA 配置”，填写 FOFA 邮箱和 API Key，保存后可测试已保存的账户配置。修改时 Key 留空保留原值；“清除配置”删除凭据。
2. 输入 FOFA 查询语句，例如 `domain="example.com" && protocol="https"`，选择每页 20/50/100 条，按需选择全部历史，再点击搜索。
3. 勾选结果，支持当前页全选、取消选择和反选；点击“同步选中”。
4. 选择目标组织。默认按结果的实际主机创建或复用 Target 并关联组织；也可选一个已有 Target，仅同步其范围内的结果。
5. 同步结果返回成功行数、登记资产数、失败原因和兼容性说明。资产在“资产与风险 → 资产”以及有对应旧类型的 Target 资产页面中可见。

查询只由搜索/翻页点击触发；返回已缓存页面复用原结果，没有焦点刷新或自动重试。FOFA 权限、额度与查询语法错误通过服务端返回。每页结果保存在服务端一小时，同步过期结果需要重新执行查询。

网络空间搜索凭据与 Subfinder 扫描数据源分别管理。管理员可配置/清除凭据；活动的 admin/analyst 成员可搜索和同步。API 不返回 Key 或密文，普通搜索账号也不读取账户邮箱。

## 字段与入库

- FOFA 基础字段包含主机、IP、端口、协议、域名、标题、服务器、地区、备案和基础传输协议；区域及查询来源保留在统一资产观测 `extra` 中。
- HTTP URL 保留原始路径、大小写、百分号及查询串；无 scheme 的 HTTP 主机按 FOFA 明确报告的协议和端口构造 URL。
- 每行可登记 IP、子域名和服务资产。IPv4 IP/端口、HTTP 站点、子域名通过原应用服务同步到旧资产表；标题和服务器字段按稀疏观测合并。
- FOFA 中的域名/IP 关联不写成已验证的 DNS 记录，也不产生漏洞。纯 IPv6 literal 目前只登记统一资产，因为上游 Target 和 HostPort 只支持 IPv4；同步结果会给出说明。
- 浏览器只提交快照 ID、行号、组织与目标。导入内容来自当前账号的服务端快照；不信任客户端提交的资产内容。
- 每行的 Target/组织绑定、原资产写入、统一资产登记和同步账本共享事务。单行失败全部回滚，其他行可继续；相同快照行与目的地的重复/并发同步返回已保存结果。

## 配置和升级

迁移 `000022_fofa_space_search` 新增 `fofa_setting`、`fofa_search` 与 `fofa_import`。FOFA Key 使用当前持久凭据密钥（`TIANQIONG_AI_ENCRYPTION_KEY` 或 `TIANQIONG_AI_ENCRYPTION_KEY_FILE`）加密，按[运行说明](05-编码进展与运行说明.md)保存该密钥。

HTTP 接口统一使用 JWT：

| 操作 | 接口 |
| --- | --- |
| 查看配置状态 | `GET /v1/fofaSettings/current` |
| 保存 / 清除凭据 | `PUT` / `DELETE /v1/fofaSettings/current` |
| 测试已保存凭据 | `POST /v1/fofaSettings/current/test` |
| 分页查询并保存快照 | `POST /v1/fofaSearches` |
| 同步已保存的行 | `POST /v1/fofaSearches/{search}/import` |

## 验证边界

后端测试使用本地 FOFA HTTP fixture 和独立临时 PostgreSQL，验证请求编码/字段、凭据保留与脱敏、禁止重定向转发、旧页面数据投影、组织关联、幂等并发、范围拒绝、故障回滚、过期和权限撤销。前端交互测试验证显式查询、分页缓存、无效行禁止选择、翻页清空选择和账号切换丢弃旧响应。

生产前端配合 HTTP fixture 的浏览器验收已通过配置保存、Key 不回显、账户测试、查询、组织选择和同步；同时检查了首次加载表单位置及桌面/390px 窄屏布局。脚本为 `frontend/scripts/tianqiong-fofa-ui-smoke.mjs`，通过 `TIANQIONG_UI_SMOKE_URL` 指定临时前端地址。

本轮未提供真实 FOFA 凭据，未向真实账户执行搜索；实际账户权限与额度需要配置后验证。
