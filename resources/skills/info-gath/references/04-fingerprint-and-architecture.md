# Web 指纹、JS 与 API

## 入口与覆盖

`fingerprint.sh` 和 `web_probe.sh` 分别委托 Python 驱动。每个 `(scheme, host, port)` 使用独立目录，避免 HTTP/HTTPS 或不同端口互相覆盖。完整用户 URL 用于主页采集；根相对路径按 URL 语义解析。

主流程默认深入分析 5 个 origin，用户提供的 URL 优先，然后选择主机及高价值名称；选取与省略数量写入 coverage。可用 `--max-web-hosts` 调整。

## 指纹

保存响应头、whatweb、WAF 线索、TLS、技术指纹和少量已知路径。nuclei 每个 origin 只运行一次技术模板 JSONL，再由同一输出生成文本。stealth 使用 whatweb 低攻击性模式，关闭 WAF payload 和 Web NSE；`--no-port-scan` 也关闭 Web NSE。

curl 响应有连接/请求超时和 2 MiB 大小上限。各工具另有阶段总时长预算，失败保留 stderr，不写成无发现。

## JS 与路径

- HTML script src、同范围爬虫和可用的 subjs/getJS 补充 JS。
- 默认最多下载 50 个 JS；外部依赖记在 `external-js-urls.txt`，不自动下载。
- `js-index.json` 保留 URL 与本地文件映射；source map 按实际 JS URL 解析，并检查 JSON 内容。
- 保留 JS 中绝对 API URL 的主机，不把其他服务的路径错误拼到当前服务。
- OpenAPI 必须有对应结构才算文档发现；不以任意 200 页面认定 Swagger。
- HTML 在秘密扫描前下载；同一类型/值的 JS、格式化 JS、source map 和 HTML 命中去重，输出掩码。
- deep 才做目录发现；可用 ffuf 时传入限速和总时长，否则使用有限数量的 GET 请求。

## 鉴权观察

默认只收集匿名响应。只有 `--validate` 才增加无效 token 比较、GraphQL 自省和 API 类模板；不自动测试默认口令。

`ANONYMOUS_DATA` 是匿名可访问的结构化数据，`REVIEW_TOKEN_DIFFERENCE` 是 token 响应差异，两者均需业务复核。登录 HTML、相同大小的响应和 JSON Content-Type 不能单独证明绕过或权限缺失。

结果保留响应类型、哈希、长度、状态码及证据路径；不将完整响应体复制到最终 Markdown。架构结论应由这些证据支撑，不生成带空白占位符的“已完成架构图”。

工具选项参考：[httpx](https://docs.projectdiscovery.io/opensource/httpx/usage)、[Katana](https://docs.projectdiscovery.io/opensource/katana/usage)、[Nuclei](https://docs.projectdiscovery.io/opensource/nuclei/running)、[ffuf](https://github.com/ffuf/ffuf/wiki/CLI-flags)。
