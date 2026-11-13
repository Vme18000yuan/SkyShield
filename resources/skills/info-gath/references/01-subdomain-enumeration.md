# 子域与 DNS

## 来源和状态

`subdomain_enum.sh` 调用 `subdomain_enum.py`，并发聚合当前可用的 subfinder、amass passive、assetfinder、crt.sh、chaos、GitHub、gau/waybackurls、theHarvester。每个来源的退出码、错误和数量写入 `raw/subdomain-status.json`；原始命令记录在 `commands.jsonl`。

命令不存在时跳过；已启动的来源失败、超时或返回无法解析的 JSON 时，阶段为 partial。第三方限流不等同于“没有资产”。

- 名称按完整域名边界过滤、规范化、去重；不靠未转义的后缀正则。
- 历史 URL 保留主机和参数，写入 `raw/wayback-params.txt`，不因此自动发起漏洞验证。
- `--fast` 不进行随机子域 DNS 查询。
- 主动模式用两个随机名称检查 wildcard，写入 `raw/wildcard.json`。相同 IP 可能承载真实站点，因此不按一个 IP 粗暴删除全部域名。
- `01-subdomains.txt` 是枚举候选；HTTP 存活数另行统计。

## 主动字典

仅 `--deep` 且非 stealth 时执行 shuffledns。通过环境变量覆盖依赖路径：

```bash
export RECON_DNS_WORDLIST=/path/to/subdomains.txt
export RECON_RESOLVERS=/path/to/resolvers.txt
bash scripts/recon.sh example.com --deep
```

缺少工具、字典或 resolver 文件时明确记为 skipped，不宣称完成主动枚举。

## 解析和 AXFR

DNS 优先读取 dnsx JSONL 的 `host/a/aaaa`，失败后降级 dig。dig 输出中的 CNAME 不作为 IP。`dns-records.json` 保留域名、记录类型和地址的关系。

AXFR 优先 dig，无 dig 时尝试 dnspython。结果写入 `axfr.json`，每个 NS 单独标记 success/refused/failed/timeout；只有实际拒绝才能据此说明被拒绝，超时不是拒绝。stealth 跳过 AXFR。
