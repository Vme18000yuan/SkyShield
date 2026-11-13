#!/usr/bin/env bash
# Find candidate origin IPs behind CDN + ASN reverse expansion.
# Usage: bash find_real_ip.sh <root-domain> [subdomains-file] [output-dir]
#   subdomains-file 默认 reports/<root>/01-subdomains.txt
#   output-dir      默认从 subdomains-file 推导,否则 reports/<root>
set -uo pipefail
umask 077
STATUS=0

ROOT="${1:?Usage: $0 <root-domain> [subdomains-file] [output-dir]}"
if [ "${RECON_MODE:-normal}" = "fast" ]; then
  echo "fast 模式跳过目标 DNS/CDN 查询"; exit 0
fi
if ! command -v dig >/dev/null 2>&1; then
  echo "dig 缺失，CDN/源站线索阶段未执行"; exit 2
fi
WHOIS_AVAILABLE=0
command -v whois >/dev/null 2>&1 && WHOIS_AVAILABLE=1
SUBS="${2:-reports/$ROOT/01-subdomains.txt}"
# OUTDIR 优先取第三参数;否则从 SUBS 路径推导(取其所在目录);再否则 reports/<root>
if [ -n "${3:-}" ]; then
  OUTDIR="$3"
elif [ "$SUBS" != "reports/$ROOT/01-subdomains.txt" ]; then
  OUTDIR="$(dirname "$SUBS")"
else
  OUTDIR="reports/$ROOT"
fi
mkdir -p "$OUTDIR/raw"

echo "# CDN 检测与真实 IP 挖掘 — ${ROOT}"
echo "_生成时间: $(date -u +%FT%TZ)_"
echo ""

# ===== 1. CDN signature lookup via dnsx =====
echo "## 1. DNS 解析（A/AAAA/CNAME）"
echo '```'
if command -v dnsx >/dev/null 2>&1 && [ -f "$SUBS" ]; then
  if [ -f "$OUTDIR/raw/dns-ip-resolve.jsonl" ]; then
    head -60 "$OUTDIR/raw/dns-ip-resolve.jsonl"
  else
    dnsx -l "$SUBS" -a -cname -resp -silent -o "$OUTDIR/raw/dnsx-cdn.txt" 2>"$OUTDIR/raw/dnsx-cdn.err"
    head -60 "$OUTDIR/raw/dnsx-cdn.txt"
  fi
else
  echo "(dnsx 未安装或子域文件不存在)"
fi
echo '```'
echo ""

# ===== 2. ASN 自动化判定 =====
echo "## 2. 根域 ASN 归属判定"
echo '```'
ROOT_IP=$(dig +short +time=3 +tries=1 A "$ROOT" 2>/dev/null | python3 -c 'import ipaddress,sys
for line in sys.stdin:
 try:
  ip=ipaddress.ip_address(line.strip())
  if ip.version == 4: print(ip); break
 except ValueError: pass')
if [ -n "$ROOT_IP" ]; then
  ASN_INFO=""
  if [ "$WHOIS_AVAILABLE" = 1 ]; then
    ASN_INFO=$(timeout --foreground 15 whois -h whois.cymru.com " -v $ROOT_IP" 2>/dev/null | tail -1)
  fi
  ASNUM=$(printf '%s\n' "$ASN_INFO" | cut -d '|' -f1 | grep -oE '[0-9]+' | head -1)
  if [ -z "$ASNUM" ]; then
    # Query the same provider's DNS interface only for globally routable IPv4;
    # internal addresses must not be sent to an external metadata provider.
    REVERSED_IP=$(python3 -c 'import ipaddress,sys
ip=ipaddress.ip_address(sys.argv[1])
if ip.is_global: print(".".join(reversed(str(ip).split("."))))' "$ROOT_IP")
    if [ -n "$REVERSED_IP" ]; then
      ASN_INFO=$(dig +short +time=3 +tries=1 TXT "${REVERSED_IP}.origin.asn.cymru.com" 2>/dev/null | head -1 | tr -d '"')
      ASNUM=$(printf '%s\n' "$ASN_INFO" | cut -d '|' -f1 | grep -oE '[0-9]+' | head -1)
      [ -n "$ASNUM" ] && echo "ASN 来源: Team Cymru DNS"
    fi
  fi
  [ -n "$ASNUM" ] || STATUS=2
  echo "Root IP: $ROOT_IP"
  echo "ASN:     $ASN_INFO"
  # Known CDN ASN flags
  CDN_ASN=""
  case "$ASNUM" in
    13335) CDN_ASN="Cloudflare" ;;
    16625|20940) CDN_ASN="Akamai" ;;
    36459) CDN_ASN="Fastly" ;;
  esac
  echo "$ASN_INFO" | grep -qiE 'cloudflare' && CDN_ASN="Cloudflare"
  echo "$ASN_INFO" | grep -qiE 'akamai' && CDN_ASN="Akamai"
  echo "$ASN_INFO" | grep -qiE 'fastly' && CDN_ASN="Fastly"
  [ -n "$CDN_ASN" ] && echo "[CDN] $CDN_ASN 检测到" || echo "[可能源站或未知 CDN]"
else
  echo "(无法解析 $ROOT)"
fi
echo '```'
echo ""

# ===== 2b. ASN → IP 段反向展开（仅非 CDN 段）=====
echo "## 2b. ASN 网段反向展开"
echo ""
echo "_对非 CDN 的源站 IP，列出其路由 ASN 公告网段作为待核对线索，不推定为同组织资产_"
echo ""
echo '```'
if [ -n "${ROOT_IP:-}" ] && [ -z "${CDN_ASN:-}" ]; then
  # 提取 ASN 号
  PREFIX_FILE="$OUTDIR/raw/asn-prefixes.txt"
  : > "$PREFIX_FILE"
  if [ -n "$ASNUM" ]; then
    echo "解析 IP 的路由 ASN（不等于组织自有资产）: AS${ASNUM}"
    if [[ "${RECON_MANAGED_PROXY:-0}" != "1" ]] && command -v asnmap >/dev/null 2>&1; then
      echo "(asnmap 展开)"
      asnmap -a "AS${ASNUM}" -silent 2>/dev/null | sort -u > "$PREFIX_FILE" || true
    else
      # 无 key 兜底:RADB 路由数据库查 origin ASN 的所有 route
      if [ "$WHOIS_AVAILABLE" = 1 ]; then
        echo "(whois RADB 展开)"
        timeout --foreground 15 whois -h whois.radb.net -- "-i origin AS${ASNUM}" 2>/dev/null \
          | grep -iE '^route6?:' | awk '{print $2}' | sort -u > "$PREFIX_FILE" || true
      fi
      # 二次兜底:bgp.he.net（HTML 抓取 prefix）
      if [ ! -s "$PREFIX_FILE" ]; then
        curl -s --max-time 20 -A "Mozilla/5.0" "https://bgp.he.net/AS${ASNUM}#_prefixes" 2>/dev/null \
          | grep -oE '/net/[0-9.]+/[0-9]+' | sed 's|/net/||' | sort -u > "$PREFIX_FILE" || true
      fi
      [ -s "$PREFIX_FILE" ] || STATUS=2
    fi
    PFX_COUNT=$(wc -l < "$PREFIX_FILE" 2>/dev/null || echo 0)
    echo "展开网段数: ${PFX_COUNT} → raw/asn-prefixes.txt"
    head -20 "$PREFIX_FILE" 2>/dev/null
    [ "$PFX_COUNT" -gt 20 ] && echo "... (共 ${PFX_COUNT} 条，见文件)"
    echo ""
    echo "ASN 网段仅作关联线索，不自动进行主机发现或加入扫描范围。"
    echo "对明确授权的网段，请单独使用 recon.sh <CIDR>。"
  else
    echo "(无法确定 ASN 号，跳过展开)"
  fi
else
  echo "(根域走 CDN 或无解析 IP，跳过 ASN 展开以避免误扫 CDN 整段)"
fi
echo '```'
echo ""

# ===== 3. Multi-resolver cross check =====
echo "## 3. 多 Resolver 交叉解析（Anycast/CDN 暗示）"
echo '```'
declare -A RESOLVER_RESULTS
for dns in 8.8.8.8 1.1.1.1 114.114.114.114 223.5.5.5 9.9.9.9 208.67.222.222; do
  ips=$(dig +short +time=3 +tries=1 "$ROOT" "@${dns}" 2>/dev/null | tr '\n' ' ')
  printf "%-20s -> %s\n" "$dns" "${ips:-<none>}"
  RESOLVER_RESULTS["$dns"]="$ips"
done
# Check if all resolvers return same IP (=> less likely CDN anycast)
UNIQ_IPS=$(for dns in "${!RESOLVER_RESULTS[@]}"; do echo "${RESOLVER_RESULTS[$dns]}"; done | tr ' ' '\n' | grep -oP '\d+\.\d+\.\d+\.\d+' | sort -u | wc -l)
echo ""
echo "不同 IP 数量: ${UNIQ_IPS} （>3 通常是 CDN Anycast）"
echo '```'
echo ""

# ===== 4. MX / SPF / TXT =====
echo "## 4. 邮件基础设施（MX/SPF — 通常不经过 CDN）"
echo '```'
echo "--- MX ---"
MX_HOSTS=$(dig +short MX "$ROOT" 2>/dev/null)
echo "$MX_HOSTS"
echo ""
echo "--- MX IP ---"
echo "$MX_HOSTS" | awk '{print $2}' | while read -r mxh; do
  [ -z "$mxh" ] && continue
  mxip=$(dig +short A "$mxh" 2>/dev/null | head -1)
  [ -n "$mxip" ] && echo "$mxh -> $mxip"
done
echo ""
echo "--- SPF ip4 段 ---"
dig +short TXT "$ROOT" 2>/dev/null | grep -iE 'v=spf' | tr ' ' '\n' | grep -oP 'ip4:\K[\d./]+|ip6:\K[\da-fA-F:./]+'
echo '```'
echo ""

# ===== 5. Non-CDN sub patterns =====
echo "## 5. 可能未走 CDN 的子域"
echo '```'
if [ -f "$SUBS" ]; then
  grep -iE '^(dev|test|uat|stage|staging|pre|origin|direct|backend|internal|vpn|mail|smtp|ftp|cpanel|panel|admin|jenkins|gitlab|api-internal)\.|[-.](dev|test|uat|origin|direct)\.' "$SUBS" \
    | while read -r sub; do
        ips=$(dig +short A "$sub" 2>/dev/null | head -3 | tr '\n' ' ')
        [ -n "$ips" ] && printf "%-40s -> %s\n" "$sub" "$ips"
      done
fi
echo '```'
echo ""

# ===== 6. Historical resolution =====
echo "## 6. 历史 DNS 查询（手动）"
echo ""
echo "- SecurityTrails: https://securitytrails.com/list/apex_domain/${ROOT}"
echo "- ViewDNS:        https://viewdns.info/iphistory/?domain=${ROOT}"
echo "- DNSDB:          (需 API key)"
echo ""

# ===== 7. Network space query hints =====
echo "## 7. 网络空间测绘查询语法"
echo ""
echo '**FOFA**:'
echo '```'
echo "domain=\"${ROOT}\""
echo "cert=\"${ROOT}\""
echo "title=\"<网站标题特征>\""
echo "icon_hash=\"<mmh3_hash>\"  # 见 raw/favicon-hash.txt"
echo '```'
echo ""
echo '**Shodan**:'
echo '```'
echo "ssl.cert.subject.cn:${ROOT}"
echo "hostname:${ROOT}"
echo "http.favicon.hash:<mmh3_hash>"
echo '```'
echo ""
echo '**Quake (奇安信)**:'
echo '```'
echo "domain:\"${ROOT}\""
echo "cert:\"${ROOT}\""
echo '```'
echo ""
echo '**Hunter (奇安信)**:'
echo '```'
echo "domain=\"${ROOT}\""
echo '```'
echo ""

# ===== 8. Validation snippet =====
echo "## 8. 源站 IP 验证模板"
echo ""
echo "对每个候选 IP 用 Host 头验证："
echo '```bash'
echo "CANDIDATE_IP=203.0.113.10"
echo "HOST=www.${ROOT}"
echo "# Step1: HTTP 状态 + 头"
echo "curl -ksI --resolve \"\${HOST}:443:\${CANDIDATE_IP}\" \"https://\${HOST}/\""
echo "# Step2: Title 对比"
echo "TITLE_CDN=\$(curl -ks \"https://\${HOST}/\" | grep -oP '<title>\\K[^<]+' | head -1)"
echo "TITLE_DIRECT=\$(curl -ks --resolve \"\${HOST}:443:\${CANDIDATE_IP}\" \"https://\${HOST}/\" | grep -oP '<title>\\K[^<]+' | head -1)"
echo "echo \"CDN: \$TITLE_CDN | Direct: \$TITLE_DIRECT\""
echo "# Step3: TLS 证书 SAN"
echo "echo | openssl s_client -connect \"\${CANDIDATE_IP}:443\" -servername \"\${HOST}\" 2>/dev/null | openssl x509 -noout -ext subjectAltName"
echo '```'

exit "$STATUS"
