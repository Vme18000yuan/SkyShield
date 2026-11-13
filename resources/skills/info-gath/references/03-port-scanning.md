# 端口与服务

`port_scan.sh` 是 `scan_ports.py` 的兼容入口，输入为一个 IP/CIDR 或逐行目标文件。

| 模式/条件 | 发现阶段 | 服务识别 |
|---|---|---|
| normal + nmap | 常见 100 TCP 端口 | 对实际发现的每个主机单独识别 |
| deep + 有权限的 masscan | 1–65535，JSON 输出 | nmap 对每台主机的已发现 TCP 端口识别 |
| deep + nmap | 1–65535，XML 输出 | 同上 |
| 单 IP 且无可用扫描器 | Python 常见端口集合 | 简单 banner；不等同于 nmap 服务识别 |
| CIDR 且无扫描器 | failed | 不用单 IP 兜底伪装完成网段扫描 |

不自动调用 sudo；nmap 使用 TCP connect。IPv6 自动添加 `-6`。默认不运行漏洞类 NSE 或全端口 UDP。`--no-port-scan` 同时关闭 Web 指纹中的 nmap。

## 证据

- `raw/ports.json`：标准化服务记录。
- `raw/port-scan-status.json`：每个目标的后端、状态及覆盖范围。
- `raw/nmap-*.xml`：按 XML host/address/port 提取，只有 state=open 才算确认开放。
- `raw/tcp-*.json`：通过文件内部 target 识别主机，不用文件名猜地址。

两个主机的 443 是两个服务、一个唯一协议/端口。报告同时提供两类计数。网络目标自身不计为一个主机。

## Python 入口

```bash
python3 scripts/port_scan.py -t 203.0.113.10 --profile web --banner -o result.json
python3 scripts/port_scan.py -t 203.0.113.10 --ports 8000-8010 --workers 10 --delay 0.1
```

`full` 是内置 profile 的并集，约 90 个常见端口，不是全端口。`--delay` 控制 TCP 探测的提交间隔。恢复状态绑定规范化目标；目标未知或不一致时拒绝复用。

参考：[Nmap XML](https://nmap.org/book/output-formats-xml-output.html)、[IPv6 扫描](https://nmap.org/book/port-scanning-ipv6.html)、[masscan 输出格式](https://github.com/robertdavidgraham/masscan)。
