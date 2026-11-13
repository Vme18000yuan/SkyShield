# 工具能力与环境

编排和报告使用 Python 3.10+ 标准库；Shell 入口使用 bash，文件锁使用 Linux flock。主流程不修改系统工具或 PATH。

```bash
bash scripts/check_tools.sh
bash scripts/check_tools.sh --require httpx dnsx
bash scripts/check_tools.sh --json
```

httpx/dnsx 必须通过实际 CLI 能力检查；Python 的同名 httpx 命令不能替代 ProjectDiscovery httpx。工具运行失败也会触发 HTTP/DNS 降级，并保留失败记录。

| 能力 | 首选/可选 | 降级及限制 |
|---|---|---|
| 第三方被动源 | subfinder、amass、assetfinder、gau 等 | 可用来源加 crt.sh；失败来源单独列出 |
| DNS | ProjectDiscovery dnsx | dig；两者均不可用时阶段失败 |
| AXFR | dig | dnspython；无能力时为 partial |
| HTTP | ProjectDiscovery httpx | curl |
| 端口 | masscan / nmap | 单 IP 用 Python 常见端口集合；CIDR 不伪装降级成功 |
| Web 技术 | whatweb、nuclei、wafw00f | 响应头、TLS、已知路径观察 |
| JS | katana、subjs、getJS、LinkFinder、js-beautify | HTML script 标签和内置端点提取 |
| 前端候选 | 内置正则 | 不要求额外秘密扫描器；只报告候选及掩码 |
| 目录 | ffuf（deep） | 有限 GET 路径集合 |
| 组织注册 | WHOIS / RDAP | 状态与原始证据保存；tldextract 可选，使用内置 PSL 快照 |

Kali 安装参考（手动确认本机包名与版本）：`apt install curl dnsutils whois nmap masscan openssl`。ProjectDiscovery 工具按官方文档安装经过验证的版本；不依赖每次运行自动更新到 latest。

可选 Python 依赖：`dnspython`（无 dig 时 AXFR）、`tldextract`（完整 public suffix 解析）、`mmh3`（favicon 哈希）、`tqdm`（直接运行 TCP scanner 的进度条）。缺少可选依赖不会自动安装。

环境变量：`PDCP_API_KEY`、`GITHUB_TOKEN`、`COS_APP_ID` 按工具实际需要使用；不打印值。subfinder 的 provider 配置使用其官方配置文件，不假定导出任意 API 环境变量即会被读取。

字典覆盖：`RECON_DNS_WORDLIST`、`RECON_RESOLVERS`、`RECON_WEB_WORDLIST`。阶段预算由主入口配置。批量总请求量是并发作业的合计，`--http-rate` 不是跨进程共享的全局限额。
