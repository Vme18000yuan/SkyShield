# go-mitmproxy 来源说明

本目录取自 `github.com/lqqyt2423/go-mitmproxy` v1.9.2，使用随附的 MIT 许可证。仅复制录制代理需要的 `cert`、`internal` 和 `proxy` 包。

宿主适配包括：使用预先绑定的监听器、关闭代理时同步结束内部 TLS 服务器、无 SNI 的 IP 地址使用 CONNECT 目标生成证书、正确处理上游代理默认端口与特殊字符凭据，并在托管模式验证 HTTPS 上游代理证书。代理测试对 Go 新版证书错误使用类型判断。
