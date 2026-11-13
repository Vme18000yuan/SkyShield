---
name: info-gath
description: 在 Linux 上对用户明确指定的授权域名、URL、IP 或 CIDR 进行信息收集与攻击面盘点，聚合子域、DNS、端口服务、Web/JS/API 和云存储候选，生成带执行状态及证据的中文、英文和 JSON 报告。适用于单目标侦察和批量资产测绘。
metadata:
  version: 3.0.0
  environment: linux
---

# 信息收集与攻击面盘点

## 执行入口

在用户已确认目标范围的前提下，使用本 Skill 的脚本完成采集。脚本路径相对于本文件；报告默认写入调用时工作目录的 `reports/`。不要因缺少可选工具而自动安装或修改系统配置。

```bash
bash /path/to/info-gath/scripts/check_tools.sh
bash /path/to/info-gath/scripts/recon.sh example.com
bash /path/to/info-gath/scripts/recon.sh https://example.com:8443/app
bash /path/to/info-gath/scripts/recon.sh 203.0.113.0/24 --deep
bash /path/to/info-gath/scripts/batch_recon.sh --targets targets.txt --agents 3 --fast
```

`targets.txt` 每行一个目标，支持注释和没有末尾换行的文件。批量入口按规范化后的主机/网段去重，并合并同主机 URL 入口；`--agents` 指并发进程数，范围 1–5。

## 模式与开关

| 设置 | 行为 |
|---|---|
| `--fast` | 仅第三方被动源及注册/证书信息；不执行目标 DNS、AXFR、HTTP、端口、Web 或云桶探测 |
| 默认 normal | 被动枚举、DNS、AXFR、CDN/ASN 线索、HTTP、常见端口、技术指纹、JS/API 观察和有预算的云桶探测 |
| `--deep` | 增加字典子域枚举、全 TCP 端口和目录发现；依赖不足时在报告中注明覆盖限制 |
| `--stealth` | HTTP 限速至最多 2 次/秒，关闭 AXFR、Web NSE、目录爆破、云桶猜测和 token 比较；与 deep 合用时仍做降速全端口 |
| `--no-port-scan` | 关闭端口阶段及指纹脚本中的 nmap |
| `--no-cloud` | 不主动探测猜测的云桶；集成场景默认使用，避免把关联线索扩展为扫描范围 |
| `--validate` | 增加无效 token 响应比较、GraphQL 自省和 API 类模板；不能与 stealth 同用 |
| `--rate N` | 端口扫描速率上限；不代表全流程 HTTP 速率 |
| `--http-rate N` | 单作业 HTTP/DNS 工具速率配置，默认 10；批量总量受作业数影响 |
| `--max-web-hosts N` | 深入分析的 Web origin 数，默认 5；发现数、选中数与省略数写入 coverage |
| `--stage-timeout N` | 每阶段总时长预算，默认 300 秒；超时记录并终止子进程 |
| `--cloud-max-requests N` | 云桶请求总数上限，默认 80 |
| `--cloud-budget-seconds N` | 云桶总时长预算，默认 60 秒 |
| `--output-dir DIR` | 自定义报告根目录 |
| `--force` | 保留旧入口兼容性；所有运行均新建独立 run，不自动复用旧文件 |

端口后端覆盖：deep 优先 masscan（有权限时），其次 nmap；normal 使用 nmap 常见 100 端口。缺少扫描器时，单 IP 用 Python 常见端口集合兜底；该集合名为 `full`，**不等于 65535 全端口**。CIDR 无可用扫描器时记为失败。

## 数据与运行约定

- 域名保留用户给定的主机范围，不自动扩大为注册根域。URL 保留协议、端口、路径和查询参数；IPv6 URL 使用方括号。
- IP 队列仅来自明确的 DNS A/AAAA 字段或用户 IP/CIDR 输入，不从标题、证书文本或任意字符串中抓取地址。已识别的 CDN IP 单独保存，不进入域名的全端口队列。
- 组织关联域名和 ASN 网段是待核对线索，不自动加入扫描范围。对已授权网段，另用 CIDR 入口执行。
- 匿名 JSON、HTTP 200、token 响应差异和公开桶列举均为观察或待复核项。未提供业务权限基线时，不生成“已确认未授权”结论。
- JS 下载限制在目标域名/IP 范围内；外部依赖只记候选。响应和 source map 有大小限制，原始文件仅存于受限报告目录。
- 工具缺失、执行失败、超时、主动跳过和成功无发现应分别记录。退出码：`0` 完成，`2` 部分完成或输入错误，`3` 同目标被锁定，`130` 中断；报告生成失败为 `1`。

## 交付与复核

```text
reports/<target_id>/
├── latest -> runs/<run_id>/
├── recon.json                       # 指向 latest 的兼容入口
├── 信息收集报告_<target_id>.md
└── runs/<run_id>/
    ├── manifest.json                # 模式、阶段状态、覆盖范围
    ├── commands.jsonl               # 命令、退出码、耗时及 stderr 路径
    ├── recon.json
    ├── skyshield-import.json         # 统一资产、漏洞候选及证据引用
    ├── 08-recon-summary.md
    ├── 信息收集报告_<target_id>.md
    └── raw/
```

每个目标使用文件锁；已存在的旧式产物在首次发布时迁移到 `runs/legacy-*`，不混入本次结果。批量汇总使用本批次的 run 状态，不以“报告文件存在”判断成功。

完成后读取 `recon.json` 和中文报告，向用户汇报：执行状态、资产及服务数量、覆盖限制、待复核发现和产物路径。正文引用摘要，原始响应通过证据路径追溯。部分失败时明确指出未完成阶段，不输出安全性结论。

在 SkyShield 中使用时，优先调用产品提供的信息收集执行与同步能力，不要仅靠手工编辑导入 JSON；导入结果需要由系统校验用户权限和目标范围。未匹配目标、范围外或归属不明的线索保留待确认，不自行新增目标或将 CDN/云桶认作自有资产。详细字段见 [SkyShield 输出契约](references/skyshield-import.schema.json)。

SkyShield 托管执行通过产品代理转发 HTTP(S)，明确为 httpx、katana、ffuf 和 subfinder 设置代理；无法保证遵守代理的附加探测工具和 nmap 应用层指纹脚本会跳过，端口结果仍可获得，但详细服务版本覆盖受限。原始独立运行的行为保持不变。

## 按需参考

- [子域与 DNS](references/01-subdomain-enumeration.md)：来源状态、wildcard、字典设置。
- [CDN/ASN 与源站线索](references/02-cdn-bypass-real-ip.md)：归属证据及候选验证。
- [端口扫描](references/03-port-scanning.md)：后端选择、权限及覆盖范围。
- [Web 指纹与 API](references/04-fingerprint-and-architecture.md)：探测边界与结果解释。
- [工具链](references/05-kali-toolchain.md)：依赖、能力检查和安装参考。
- [输出契约](references/06-output-schema.md)：结构化字段和兼容性；机器定义见 [recon.schema.json](references/recon.schema.json)。

本地回归验证：`python3 -m unittest discover -s /path/to/info-gath/tests -v`。测试替换外部命令，不扫描外部目标。
