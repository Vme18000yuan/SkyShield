# recon.json 输出契约 2.0

中英文报告和批量摘要从同一份模型生成。机器定义为 [recon.schema.json](recon.schema.json)。

| 字段 | 语义 |
|---|---|
| `schema_version` | 当前为字符串 `2.0` |
| `target` / `target_id` | 规范化主机/网段，以及兼容目录键 |
| `entry_urls` | 用户提供的完整 URL，包括非标准端口、路径、查询参数 |
| `run_id` / `status` | 本次运行编号；completed / partial / failed / interrupted，旧产物为 unknown |
| `mode` | fast / normal / deep；stealth 是独立修饰项，详见 manifest |
| `steps` | 每阶段状态、原因、起止时间；success / partial / failed / timeout / skipped / interrupted |
| `coverage` | Web 选取/省略数、扫描后端及端口范围、云桶请求预算/实际数等 |
| `tools` | 可执行路径和能力检测结果；httpx/dnsx 校验真实 CLI 身份 |
| `subdomains` | 枚举候选，不等同于确认存活 |
| `http` / `alive` | HTTP 结构化记录 / 兼容的人类可读行 |
| `dns_records` / `cdn_excluded` | 主机、A/AAAA、IP 的观测关联；已识别 CDN 地址的单独记录 |
| `discovered_urls` | 爬虫、JS、API 文档和历史来源发现的 URL 及证据路径，可能未实际验证 |
| `ips` / `networks` | 观测到的主机 IP / 用户给定的 CIDR，分别计数 |
| `services` | 按真实 IP、protocol、整数 port 去重的开放服务，含 service、version、evidence |
| `ports` | 旧消费者兼容映射：真实 IP → `{port,protocol,service,version}`；port 保留字符串 |
| `axfr` | 逐 NS 状态与证据、successful_ns；部分拒绝不会覆盖其他 NS 的成功 |
| `org_assets.related_roots` | 去掉目标自身注册根域的关联根域候选，不自动获得扫描范围 |
| `asn_prefixes` | 路由归属线索，不等同于企业自有网段 |
| `cloud_buckets` | 去重后的云桶候选，含 source、ownership、verdict、evidence |
| `unauth_findings` | 待复核匿名接口或 token 差异；不默认输出 HIGH_UNAUTH |
| `secrets` | 去重后、脱敏的前端候选统计；公开 AppID、存储 URL 也可能是观察项 |
| `sensitive_paths` | HTTP 200 或模板命中候选，不等同于确认泄露 |
| `evidence_errors` | 损坏/不可解析证据；有错误时 completed 会降为 partial |

## 计数

- `stats.open_ports_total`：跨 IP 去重的 `(protocol, port)` 数。
- `stats.open_services_total`：`(ip, protocol, port)` 数，通常更适合资产数量统计。
- `stats.ips`：主机 IP 数，不把一个 `/24` 当作一个主机。
- Markdown 仅展示部分候选；JSON 不静默截断数组，选取预算写入 coverage。

## 观察项与置信度

`ANONYMOUS_DATA` 表示匿名返回结构化内容；`REVIEW_TOKEN_DIFFERENCE` 表示无效 token 返回不同结构化响应。两者都需要业务权限基线，不表示已确认漏洞。`SPA_FALLBACK` 要求响应内容签名一致，不仅比较长度。

云桶 `PUBLIC_LISTING` 要求解析到服务商列举 XML；公开静态资源可能为预期配置，需确认归属和数据敏感性。403 仅记录为受限响应线索。

## 兼容与迁移

保留 `stats/subdomains/alive/ips/ports` 及主要扩展字段；新增 `services/steps/coverage` 等。原先把 CIDR 或文件名当作 IP 的错误数据不会继续生成。旧 raw 文件可通过 `build_report.sh <target_id>` 重建报告；缺少 manifest 时状态为 unknown，不能补推未执行检查。

每次运行独立保存，不复用旧文件。稳定路径指向 latest；历史版本使用 `runs/<run_id>/recon.json`。

## SkyShield 集成契约 1.0

每次生成报告时同时写入 `skyshield-import.json`，定义见 [skyshield-import.schema.json](skyshield-import.schema.json)。它保留 `run` 状态，以及 `assets`、`vulnerabilities` 和 `observations` 三个独立数组。记录的 SHA-256 `id` 根据规范化的类型与身份字段计算，重复执行转换时保持稳定；同一采集运行的身份由 `run.id` 区分。

`domain` 是未经注册根域判断的域名，导入方应结合公共后缀表区分根域和子域。`ip` 的 `bound_domains` 来自实际 A/AAAA 记录；非 HTTP `service` 的 `value` 是 IP，另有 `port`、`protocol`、`service_name`。HTTP `service` 和 `endpoint` 的 `value` 是 URL；服务可携带已清理的 `page_title` 与 `technologies`，用于应用归集。来自爬虫、JS 和历史来源的 URL 标记 `confidence=candidate`，不代表已验证存活。为避免传播凭据，导入 URL 不包含用户名、密码、查询串或片段，证据只使用报告内相对路径；原始证据应保存在受访问控制的运行目录中。

`scope` 仅是对本次采集输入的提示，**不是导入授权**：`associated` 表示有 DNS 关联，不代表 IP 所有权；`unverified` 包括 CDN 和云桶线索。消费者必须基于系统目标和当前用户权限逐项重新校验，不能仅凭该字段或云厂商域名自动归属。关联根域、ASN 和云桶不能自动扩展采集范围。

`vulnerabilities` 当前仅含匿名结构化响应及令牌响应差异的低严重度待复核候选，始终为 `confidence=candidate`、`status=needs_review`，不能表述为已确认漏洞；公开云桶和 DNS 区域传送记录为 `observations`，分别需要所有权/敏感性或协议层复核。空数组不代表已证明安全。导入失败和范围外记录须保留以供用户选择目标后复核，不得静默丢弃。
