# CDN/ASN 与源站候选

## 归属与验证

`find_real_ip.sh` 收集 DNS、路由 ASN、邮件/SPF、非典型子域等线索，并给出历史 DNS 和空间测绘的查询入口。它输出候选和验证模板，不自动完成源站确认。

- 云服务商 ASN 只能说明网络归属，不能单独证明使用 CDN，也不能证明整个 ASN 归目标企业所有。
- 根域解析可能含 CNAME，应先验证地址类型。
- 主流程将 HTTP 工具明确识别的 CDN 地址写入 `cdn-excluded.json`，不加入域名端口扫描队列。无法识别的 CDN 仍可能存在，应结合 CNAME/响应头复核。
- ASN 公告网段只记录在 `raw/asn-prefixes.txt`，不自动扫描。对确认授权的网段使用独立 CIDR 任务。

## 候选确认

在目标范围允许时，针对一个候选 IP 使用原主机的 Host/SNI 发起请求，保存响应头、正文摘要、证书和页面特征，与正常入口比较。IPv6 候选地址使用方括号。

```bash
HOST=www.example.com
CANDIDATE=203.0.113.10
curl --connect-timeout 3 --max-time 8 --resolve "${HOST}:443:${CANDIDATE}" "https://${HOST}/"
```

仅连通、返回 200 或标题一致不足以确认源站。共享主机、默认页、负载均衡和跳回 CDN 都可能产生误判；跨主机跳转需重新核对范围。

## 第三方线索

FOFA、Shodan、SecurityTrails、ViewDNS 等需按实际服务权限和当前语法使用。自动化脚本不把“显示查询链接”计作已经完成平台查询。
