#!/usr/bin/env python3
"""Scan collected assets; masked candidates, deduplicated by type/value."""
import re, sys, os, glob, json

js_dir      = sys.argv[1]
secrets_out = sys.argv[2]
probe_dir   = sys.argv[3]

RULES = [
    # Cloud Keys
    ("AWS_ACCESS_KEY",       r'AKIA[0-9A-Z]{16}'),
    ("AWS_SECRET_KEY",       r'(?i)aws.{0,20}secret.{0,10}["\s:=]+([A-Za-z0-9/+=]{40})'),
    ("GCP_API_KEY",          r'AIza[0-9A-Za-z\-_]{35}'),
    ("GCP_OAUTH",            r'[0-9]+-[0-9A-Za-z_]{32}\.apps\.googleusercontent\.com'),
    ("AZURE_CLIENT_SECRET",  r'(?i)(?:client.?secret|AZURE.{0,10}SECRET).{0,20}["\s:=]+([A-Za-z0-9/+=\-]{30,})'),
    # Generic Keys / Tokens
    ("GENERIC_API_KEY",      r'(?i)(?:api[_-]?key|apikey|access[_-]?key|secret[_-]?key)\s*[=:]\s*["\']([A-Za-z0-9_\-]{20,64})["\']'),
    ("GENERIC_TOKEN",        r'(?i)(?:token|access_token|refresh_token|auth_token)\s*[=:]\s*["\']([A-Za-z0-9_\-\.]{20,128})["\']'),
    # JWT
    ("JWT",                  r'eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}'),
    ("JWT_SECRET",           r'(?i)(?:jwt[_-]?secret|jwt[_-]?key|secret[_-]?key)\s*[=:]\s*["\']([^"\']{8,})'),
    # Private Keys
    ("PRIVATE_KEY",          r'-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----'),
    # Database URIs
    ("DB_CONN_STRING",       r'(?i)(?:jdbc:|mongodb(?:\+srv)?://|redis://|postgresql://|mysql://|postgres://)[^\s"\'<>{}\[\]]{10,150}'),
    # Storage
    ("S3_URL",               r'https?://[a-zA-Z0-9\-]+\.s3(?:\.[a-z0-9\-]+)?\.amazonaws\.com'),
    ("ALIYUN_OSS",           r'https?://[a-zA-Z0-9\-]+\.oss-cn-[a-z0-9\-]+\.aliyuncs\.com'),
    ("QINIU",                r'https?://[a-zA-Z0-9\-]+\.(?:qiniu|qbox)\.com'),
    # Social / Payment
    ("WECHAT_APPID",         r'wx[a-f0-9]{16}'),
    ("WECHAT_MCHID",         r'(?<!\d)1[0-9]{9}(?!\d)'),
    ("ALIPAY_APPID",         r'(?<!\d)201[0-9]{13}(?!\d)'),
    # VCS Tokens
    ("GITHUB_TOKEN",         r'ghp_[A-Za-z0-9]{36}'),
    ("GITHUB_OAUTH",         r'gho_[A-Za-z0-9]{36}'),
    ("GITLAB_TOKEN",         r'glpat-[A-Za-z0-9\-_]{20}'),
    # Comms
    ("SLACK_TOKEN",          r'xox[baprs]-[A-Za-z0-9\-]{10,}'),
    ("SLACK_WEBHOOK",        r'https://hooks\.slack\.com/services/T[A-Z0-9]+/B[A-Z0-9]+/[A-Za-z0-9]+'),
    ("TELEGRAM_BOT",         r'[0-9]{8,10}:[A-Za-z0-9_\-]{35}'),
    # Internal Networks
    ("INTERNAL_IP_RFC1918",  r'(?<![0-9])(?:10\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}|172\.(?:1[6-9]|2[0-9]|3[01])\.[0-9]{1,3}\.[0-9]{1,3}|192\.168\.[0-9]{1,3}\.[0-9]{1,3})(?![0-9])'),
    ("INTERNAL_DOMAIN",      r'[a-zA-Z0-9\-]+\.(?:internal|corp|intra|lan|local|dev\.internal|staging\.internal)(?:[:/\s]|$)'),
    # Passwords in config
    ("HARDCODED_PASSWORD",   r'(?i)(?:password|passwd|pwd)\s*[=:]\s*["\']([^"\']{6,64})["\']'),
    # App-specific
    ("FIREBASE_KEY",         r'AAAA[A-Za-z0-9_-]{7}:[A-Za-z0-9_-]{140}'),
    ("MAPBOX_TOKEN",         r'pk\.[A-Za-z0-9]{60,}\.[A-Za-z0-9_\-]{22}'),
    ("STRIPE_KEY",           r'(?:sk|pk)_(?:live|test)_[A-Za-z0-9]{24,}'),
    ("SENDGRID_KEY",         r'SG\.[A-Za-z0-9_\-]{22}\.[A-Za-z0-9_\-]{43}'),
    # Global config objects
    ("GLOBAL_CONFIG_OBJ",    r'window\.__(?:INITIAL_STATE|APP_CONFIG|CONFIG|ENV|SETTINGS|DATA)__\s*=\s*\{[^}]{10,}'),
]

results = []
seen = set()

def scan_file(filepath, label):
    try:
        content = open(filepath, encoding='utf-8', errors='replace').read(2097152)
    except Exception:
        return
    lines = content.splitlines()
    for rule_name, pattern in RULES:
        for i, line in enumerate(lines, 1):
            for m in re.finditer(pattern, line, re.IGNORECASE):
                val = m.group(1) if m.lastindex else m.group(0)
                key = (rule_name, val)
                if key in seen: continue
                seen.add(key)
                # Mask sensitive value: keep first 6 + last 4 chars
                masked = '***' if len(val) <= 12 else val[:6] + '***' + val[-4:]
                results.append({
                    "type":   rule_name,
                    "file":   label,
                    "line":   i,
                    "match":  masked,
                    "length": len(val)
                })

# Scan downloaded JS files
for fpath in glob.glob(os.path.join(js_dir, '*.js')) + glob.glob(os.path.join(js_dir, '*.pretty')):
    scan_file(fpath, os.path.basename(fpath))

# Scan source maps
for fpath in glob.glob(os.path.join(js_dir, '*.map')):
    scan_file(fpath, os.path.basename(fpath))

# Scan HTML pages (main + common pages)
html_dir = os.path.join(probe_dir, 'html')
for fpath in glob.glob(os.path.join(html_dir, '*.html')):
    scan_file(fpath, 'html/' + os.path.basename(fpath))

# Write JSON
with open(secrets_out, 'w', encoding='utf-8') as f:
    json.dump({
        "total": len(results),
        "summary": {r[0]: sum(1 for x in results if x["type"]==r[0]) for r in RULES if any(x["type"]==r[0] for x in results)},
        "findings": results
    }, f, indent=2, ensure_ascii=False)

print(f"[secrets] 发现: {len(results)} 条 (涉及 {len(set(r['type'] for r in results))} 种类型)")
