"""Offline regression tests. External tools are replaced by isolated PATH fixtures."""
from __future__ import annotations
import contextlib
import fcntl
import importlib.util
import io
import ipaddress
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'scripts'))
from recon_common import normalize_target, parse_dns, parse_grepable, parse_nmap_xml, run_command
from recon_report import build_report, collect_ports
from web_analysis import Response, classify
from cloud_buckets import classify as bucket_classify
import port_scan
import scan_ports
import fingerprint_driver
import web_batch
import web_probe_driver

MOCK = r'''#!/usr/bin/python3
import hashlib, ipaddress, json, os, re, sys
from pathlib import Path
from urllib.parse import urlsplit
name=Path(sys.argv[0]).name; args=sys.argv[1:]
with open(os.environ['MOCK_TRACE'],'a') as f: f.write(json.dumps({'tool':name,'args':args})+'\n')
if ('-h' in args and name != 'whois') or '--version' in args or '-version' in args or 'version' in args:
 if name=='httpx' and os.getenv('BROKEN_HTTPX'): print('ModuleNotFoundError',file=sys.stderr); sys.exit(1)
 print('-tech-detect -status-code -json -aaaa fixture 1.0'); sys.exit(0)
if name=='subfinder':
 if os.getenv('MOCK_SOURCE_SLEEP'):
  import subprocess,time
  child=subprocess.Popen([sys.executable,'-c','import time;time.sleep(30)'],start_new_session=True)
  Path(os.environ['MOCK_CHILD_PID']).write_text(str(child.pid)); time.sleep(30)
 print('app.example.test\nip-192.0.2.88-node.example.test'); sys.exit(0)
if name=='whois':
 print('13335 | 203.0.113.10 | 203.0.113.0/24 | US | arin | 2000-01-01 | CLOUDFLARE')
 sys.exit(0)
if name=='dig':
 if any('origin.asn.cymru.com' in value for value in args):
  if os.getenv('MOCK_CYMRU_OK'): print('"13335 | 8.8.4.0/24 | US | arin | 2000-01-01"')
 elif 'AXFR' in args:
  if '@ns2.example.test' in args: print(';; ->>HEADER<<- opcode: QUERY, status: REFUSED\n; Transfer failed.')
  else: print('example.test. 60 IN SOA ns1.example.test. hostmaster.example.test. 1 3600 600 86400 60\napp.example.test. 60 IN A 203.0.113.10\nexample.test. 60 IN SOA ns1.example.test. hostmaster.example.test. 1 3600 600 86400 60')
 elif 'NS' in args: print('ns1.example.test.\nns2.example.test.')
 elif 'AAAA' in args: print('2001:db8::10')
 elif 'A' in args and not re.search(r'[a-f0-9]{16}\.example',args[-1]): print(os.getenv('MOCK_ROOT_IP','203.0.113.10'))
 sys.exit(0)
if name=='dnsx':
 if os.getenv('BAD_DNS_JSON'):
  Path(args[args.index('-o')+1]).write_text('invalid dns data'); sys.exit(0)
 hosts=Path(args[args.index('-l')+1]).read_text().splitlines()
 records=[{'host':h,'a':['203.0.113.10'],'aaaa':['2001:db8::10']} for h in hosts]
 Path(args[args.index('-o')+1]).write_text(''.join(json.dumps(r)+'\n' for r in records)); sys.exit(0)
if name=='httpx':
 if os.getenv('FAIL_HTTPX'): print('runtime failure',file=sys.stderr); sys.exit(2)
 seeds=sys.stdin.read().splitlines(); records=[]
 for seed in seeds:
  if not seed: continue
  url=seed if '://' in seed else 'https://'+seed+'/'
  records.append({'url':url,'host':'203.0.113.10','status_code':200,'title':'portal mentions 10.9.8.7','cdn':bool(os.getenv('MOCK_CDN'))})
 Path(args[args.index('-o')+1]).write_text(''.join(json.dumps(r)+'\n' for r in records)); sys.exit(0)
if name=='nmap':
 flag='-oX' if '-oX' in args else '-oA' if '-oA' in args else '-oN'
 targets=Path(args[args.index('-iL')+1]).read_text().splitlines() if '-iL' in args else [args[args.index(flag)-1]]
 dest=Path(args[args.index(flag)+1]); dest.parent.mkdir(parents=True,exist_ok=True)
 if '-p' in args and not re.fullmatch(r'\d+(?:,\d+)*',args[args.index('-p')+1]): sys.exit(2)
 hosts=[]
 for target in targets:
  hosts+=['203.0.113.1','203.0.113.2'] if '/' in target else [target]
 if os.getenv('MOCK_NMAP_EXTRA_OUTSIDE'): hosts.append('198.51.100.99')
 xml='<nmaprun>'
 human=''
 for i,h in enumerate(hosts):
  port=6379 if i==0 else 443
  xml+=f'<host><address addr="{h}" addrtype="ipv6"/>' if ':' in h else f'<host><address addr="{h}" addrtype="ipv4"/>'
  xml+=f'<ports><port protocol="tcp" portid="{port}"><state state="open"/><service name="redis" product="fixture" version="1"/></port></ports></host>'
  human+=f'Nmap scan report for {h}\n{port}/tcp open redis fixture\n'
 xml+='</nmaprun>'
 if flag=='-oX': dest.write_text(xml)
 elif flag=='-oA': Path(str(dest)+'.xml').write_text(xml); Path(str(dest)+'.nmap').write_text(human)
 else: dest.write_text(human)
 sys.exit(0)
if name=='openssl': print('fixture TLS certificate'); sys.exit(0)
if name=='curl':
 url=next((s for s in reversed(args) if s.startswith(('http://','https://'))),'')
 parsed=urlsplit(url); path=parsed.path; code=200; ctype='application/json'
 if 'crt.sh' in url and os.getenv('MOCK_CRT_502'):
  print('curl: (22) The requested URL returned error: 502',file=sys.stderr); sys.exit(22)
 if path=='/api/data' and os.getenv('FAIL_API'): sys.exit(28)
 if 'crt.sh' in url: body=json.dumps([{'name_value':'app.example.test\nip-192.0.2.88-node.example.test'}])
 elif 'rdap.org' in url: body='{"entities":[]}'
 elif any(s in parsed.netloc for s in ('amazonaws.com','aliyuncs.com','googleapis.com','myqcloud.com','windows.net')):
  body='<ListBucketResult><Name>fixture</Name><Contents><Key>public.txt</Key></Contents></ListBucketResult>'; ctype='application/xml'
 elif '-ksI' in args: body='HTTP/1.1 200 OK\nServer: fixture\n'; ctype='text/html'
 elif path.endswith('.js'):
  body='fetch("/api/health"); fetch("/api/data"); fetch("https://vendor.invalid/api/private"); var bucket="https://observed.s3.amazonaws.com"; var key=""+("AKIA"+"A"*16)+"";'; ctype='application/javascript'
 elif path in ('/','/start','/login','/index.html'):
  body='<html><script src="/static/app.js"></script><script src="https://vendor.invalid/other.js"></script></html>'; ctype='text/html'
 elif path=='/openapi.json': body='{"openapi":"3.0.0","paths":{"/api/health":{"get":{}},"/api/delete":{"delete":{}}}}'
 elif path=='/api/health': body='{"status":"ok"}'
 elif path=='/api/data': body='{"items":[1,2]}'
 elif path=='/.git/HEAD': body='ref: refs/heads/main'; ctype='text/plain'
 else: code=404; body='{"error":"not found"}'
 if '-o' in args:
  dest=Path(args[args.index('-o')+1]); dest.parent.mkdir(parents=True,exist_ok=True); dest.write_text(body)
 else: sys.stdout.write(body)
 if '-w' in args:
  fmt=args[args.index('-w')+1]
  sys.stdout.write(fmt.replace('%{http_code}',str(code)).replace('%{content_type}',ctype))
 sys.exit(0)
sys.exit(0)
'''


class Fixture(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='recon-offline-')
        self.work = Path(self.temp.name); self.bin=self.work/'bin'; self.bin.mkdir()
        self.trace=self.work/'trace.jsonl'
        for name in ('bash','dirname','head','tail','grep','sed','cut','tr','sort','wc','mkdir','timeout','date','tee','awk','cat'):
            location=shutil.which(name)
            if location: (self.bin/name).symlink_to(location)
        (self.bin/'python3').symlink_to(sys.executable)
        for name in ('curl','dig','httpx','dnsx','nmap','openssl','subfinder','whois'):
            path=self.bin/name; path.write_text(MOCK); path.chmod(0o755)
        self.env=dict(os.environ, PATH=str(self.bin), MOCK_TRACE=str(self.trace), PYTHONDONTWRITEBYTECODE='1')
        for key in ('PDCP_API_KEY','GITHUB_TOKEN','COS_APP_ID','RECON_MODE','RECON_STEALTH','RECON_VALIDATE','RECON_NO_PORTSCAN'):
            self.env.pop(key,None)
    def tearDown(self): self.temp.cleanup()
    def run_cli(self, script, *args, env=None, timeout=45):
        return subprocess.run(['/bin/bash',str(ROOT/'scripts'/script),*map(str,args)],cwd=self.work,env=env or self.env,capture_output=True,text=True,timeout=timeout)
    def calls(self): return [json.loads(s) for s in self.trace.read_text().splitlines()] if self.trace.exists() else []
    def recon(self, *args, env=None):
        p=self.run_cli('recon.sh',*args,'--http-rate','1000','--max-web-hosts','1','--cloud-max-requests','2','--cloud-budget-seconds','3',env=env)
        base=self.work/'reports'/normalize_target(args[0])['target_id']
        return p,base, json.loads((base/'recon.json').read_text())


class ModelTests(unittest.TestCase):
    def test_web_batch_runs_origins_concurrently_with_shared_rate_budget(self):
        with tempfile.TemporaryDirectory() as tmp:
            out=Path(tmp); entries=out/'entries.json'
            entries.write_text(json.dumps([f'https://web{i}.example.test/' for i in range(5)]))
            active=0; peak=0; rates=[]; lock=threading.Lock()
            def fake_run(argv, outdir, name, *, timeout, env):
                nonlocal active,peak
                with lock:
                    active+=1; peak=max(peak,active); rates.append(int(env['RECON_HTTP_RATE']))
                time.sleep(.08)
                with lock: active-=1
                if 'web2.example.test' in str(argv[2]) and str(argv[1]).endswith('fingerprint.sh'):
                    return {'status':'timeout','returncode':124}
                return {'status':'success','returncode':0}
            with patch.object(sys,'argv',['web_batch.py',str(entries),str(out)]), patch.dict(os.environ,{'RECON_HTTP_RATE':'10','RECON_STAGE_TIMEOUT':'30','RECON_STEALTH':'0'}), patch.object(web_batch,'run_command',fake_run):
                result=web_batch.main()
            summary=json.loads((out/'raw/web-batch-status.json').read_text())
            self.assertEqual(result,2); self.assertEqual(summary['selected'],5)
            self.assertEqual(summary['attempted'],5); self.assertEqual(summary['completed'],4)
            self.assertEqual(summary['timed_out'],1); self.assertEqual(peak,3)
            self.assertTrue(all(rate==3 for rate in rates))
            self.assertTrue(any(item['web']=='success' and item['fingerprint']=='timeout' for item in summary['results']))

    def test_unreachable_web_origin_stops_after_retry(self):
        with tempfile.TemporaryDirectory() as tmp:
            out=Path(tmp); calls=[]
            def failure(*args,**kwargs):
                calls.append(args[2]); return {'name':args[2],'status':'failed','returncode':28}
            with patch.object(sys,'argv',['fingerprint_driver.py','https://example.test/',str(out)]), patch.dict(os.environ,{'RECON_MODE':'normal','RECON_HTTP_RATE':'1000'}), patch.object(fingerprint_driver,'run_command',failure):
                self.assertEqual(fingerprint_driver.main(),2)
            self.assertEqual(calls,['headers','headers-retry'])
            self.assertEqual(json.loads((out/'fingerprint-status.json').read_text())['reason'],'origin_unreachable')

            class Unreachable:
                def __init__(self,*args): self.attempted=0; self.failed=0; self.exhausted=False; self.deadline=0
                def fetch(self,*args): self.attempted+=1; self.failed+=1; return Response()
            with patch.object(sys,'argv',['web_probe_driver.py','https://example.test/',str(out/'web')]), patch.dict(os.environ,{'RECON_MODE':'normal','RECON_HTTP_RATE':'1000'}), patch.object(web_probe_driver,'HttpClient',Unreachable):
                self.assertEqual(web_probe_driver.main(),2)
            summary=json.loads((out/'web/web-probe-summary.json').read_text())
            self.assertEqual(summary['reason'],'origin_unreachable'); self.assertEqual(summary['requests'],2)

    def test_truncated_nmap_batch_retains_only_scoped_completed_hosts(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'partial.xml'
            path.write_text('<nmaprun><host><address addr="203.0.113.10" addrtype="ipv4"/><ports><port protocol="tcp" portid="443"><state state="open"/></port></ports></host><host>')
            records,hosts,complete,invalid=scan_ports.read_batch_xml(path,[ipaddress.ip_network('203.0.113.0/24')])
            self.assertFalse(complete); self.assertFalse(invalid)
            self.assertEqual(hosts,{'203.0.113.10':'success'})
            self.assertEqual([(r['ip'],r['port']) for r in records],[('203.0.113.10',443)])

    def test_url_ipv6_cidr_identity(self):
        x=normalize_target('https://EXAMPLE.test:8443/start?q=1')
        self.assertEqual(x['entry_url'],'https://example.test:8443/start?q=1')
        self.assertEqual(normalize_target('https://example.test/start')['target_id'],normalize_target('example.test')['target_id'])
        self.assertEqual(normalize_target('203.0.113.99/24')['target'],'203.0.113.0/24')
        self.assertEqual(normalize_target('https://[2001:db8::1]:8443/a')['entry_url'],'https://[2001:db8::1]:8443/a')
    def test_invalid_targets(self):
        for value in ('-x','example.test;id','999.1.1.1','https://user:secret@example.test','a b.test','https://a.test:99999','1.2.3.4/99'):
            with self.subTest(value=value), self.assertRaises(ValueError): normalize_target(value)
    def test_dns_only_answers(self):
        records=parse_dns('ip-192.0.2.88-node.example.test [A] [203.0.113.10]\n')
        self.assertEqual([r['ip'] for r in records],['203.0.113.10'])
        self.assertEqual(parse_dns('{"host":"a.test","a":["203.0.113.1"],"aaaa":["2001:db8::1"]}')[1]['ip'],'2001:db8::1')
    def test_masscan_grepable(self):
        data=parse_grepable('Timestamp: 1730611200\tHost: 203.0.113.10 ()\tPorts: 443/open/tcp//https///\n')
        self.assertEqual([(r['ip'],r['port']) for r in data],[('203.0.113.10',443)])
    def test_anonymous_json_not_vulnerability(self):
        result=classify(Response(200,'application/json',b'{"status":"ok"}'),[Response(404,'text/plain',b'404')])
        self.assertEqual(result['verdict'],'ANONYMOUS_DATA'); self.assertNotEqual(result['confidence'],'high')
    def test_same_length_json_not_spa(self):
        result=classify(Response(200,'application/json',b'{"data":"xx"}'),[Response(200,'text/html',b'abcdefghijklmn')])
        self.assertEqual(result['verdict'],'ANONYMOUS_DATA')
    def test_login_html_not_token_bypass(self):
        result=classify(Response(403,'application/json',b'{}'),[],Response(200,'text/html',b'<html>login</html>'),Response(403))
        self.assertIsNone(result)
    def test_soft404_content_match(self):
        self.assertEqual(classify(Response(200,'text/html',b'<html>same</html>'),[Response(200,'text/html',b'<html>same</html>')])['verdict'],'SPA_FALLBACK')
    def test_bucket_xml_exact_root(self):
        self.assertEqual(bucket_classify(200,b'<ListBucketResult><Name>x</Name></ListBucketResult>'),'PUBLIC_LISTING')
        self.assertNotEqual(bucket_classify(200,b'<html><Name>x</Name></html>'),'PUBLIC_LISTING')
    def test_huge_port_range_rejected(self):
        with self.assertRaises(ValueError): port_scan.parse_ports('1-999999999')
    def test_tcp_delay_applies_before_submission(self):
        starts=[]
        def probe(*args): starts.append(time.monotonic()); return args[1],False,None
        argv=['port_scan.py','-t','203.0.113.1','--ports','1-4','--workers','4','--delay','.025','--quiet']
        with patch.object(sys,'argv',argv), patch.object(port_scan,'probe',probe): port_scan.main()
        self.assertEqual(len(starts),4)
        self.assertTrue(all(b-a>=.018 for a,b in zip(starts,starts[1:])))


class RuntimeTests(Fixture):
    def test_external_source_failure_is_recorded_without_raw_url(self):
        result,base,data=self.recon('example.test','--fast',env=dict(self.env,MOCK_CRT_502='1'))
        self.assertEqual(result.returncode,2,result.stdout+result.stderr)
        self.assertIn({'source':'certificate_transparency','status':'failed','http_status':502},data['coverage']['organization']['failed_sources'])
        self.assertNotIn('https://',json.dumps(data['coverage']['organization']))

    def test_asn_dns_fallback_without_whois(self):
        (self.bin/'whois').unlink()
        env=dict(self.env,MOCK_ROOT_IP='8.8.4.4',MOCK_CYMRU_OK='1',RECON_MANAGED_PROXY='1')
        result=self.run_cli('find_real_ip.sh','example.test',self.work/'missing-subs.txt',self.work/'cdn',env=env)
        self.assertEqual(result.returncode,0,result.stdout+result.stderr)
        self.assertIn('ASN 来源: Team Cymru DNS',result.stdout)
        self.assertIn('Cloudflare',result.stdout)
        self.assertTrue(any('4.4.8.8.origin.asn.cymru.com' in call['args'] for call in self.calls() if call['tool']=='dig'))

    def test_private_ip_is_not_sent_to_external_asn_dns(self):
        (self.bin/'whois').unlink()
        env=dict(self.env,MOCK_ROOT_IP='10.0.0.10',MOCK_CYMRU_OK='1',RECON_MANAGED_PROXY='1')
        result=self.run_cli('find_real_ip.sh','example.test',self.work/'missing-subs.txt',self.work/'cdn',env=env)
        self.assertEqual(result.returncode,2,result.stdout+result.stderr)
        self.assertFalse(any(any('origin.asn.cymru.com' in value for value in call['args']) for call in self.calls() if call['tool']=='dig'))

    def test_managed_port_scan_batches_many_addresses_and_preserves_scope(self):
        targets=[f'203.0.113.{i}' for i in range(1,63)]+[f'2001:db8::{i:x}' for i in range(1,8)]
        target_file=self.work/'addresses.txt'; target_file.write_text('\n'.join(targets)+'\n')
        out=self.work/'batch-ports'
        env=dict(self.env,RECON_MANAGED_PROXY='1',RECON_STAGE_TIMEOUT='30',MASSCAN_RATE='2000')
        result=subprocess.run([sys.executable,str(ROOT/'scripts/scan_ports.py'),str(target_file),str(out)],capture_output=True,text=True,env=env,timeout=15)
        self.assertEqual(result.returncode,0,result.stdout+result.stderr)
        status=json.loads((out/'raw/port-scan-status.json').read_text())
        self.assertEqual(status['total_targets'],69); self.assertEqual(status['completed_targets'],69)
        self.assertEqual(status['service_detail'],'disabled_for_http_proxy')
        calls=[item for item in self.calls() if item['tool']=='nmap']
        self.assertEqual(len(calls),2)
        self.assertTrue(all('-iL' in item['args'] and '-F' in item['args'] and '--max-rate' in item['args'] for item in calls))
        self.assertEqual(sum('-6' in item['args'] for item in calls),1)
        self.assertEqual(len(json.loads((out/'raw/ports.json').read_text())),69)

    def test_managed_port_scan_rejects_unrequested_batch_addresses(self):
        target_file=self.work/'addresses.txt'; target_file.write_text('203.0.113.10\n')
        out=self.work/'batch-scope'
        env=dict(self.env,RECON_MANAGED_PROXY='1',RECON_STAGE_TIMEOUT='30',MOCK_NMAP_EXTRA_OUTSIDE='1')
        result=subprocess.run([sys.executable,str(ROOT/'scripts/scan_ports.py'),str(target_file),str(out)],capture_output=True,text=True,env=env,timeout=15)
        self.assertEqual(result.returncode,2,result.stdout+result.stderr)
        ports=json.loads((out/'raw/ports.json').read_text())
        self.assertEqual({item['ip'] for item in ports},{'203.0.113.10'})
        self.assertEqual({item['ip'] for item in collect_ports(out,[])},{'203.0.113.10'})
    def test_fast_is_passive(self):
        p,base,data=self.recon('example.test','--fast')
        self.assertEqual(p.returncode,0,p.stdout+p.stderr)
        self.assertEqual(data['status'],'completed')
        self.assertFalse(any(c['tool'] in ('dig','dnsx','httpx','nmap','openssl') for c in self.calls()))
        self.assertEqual(data['steps']['http']['status'],'skipped')
    def test_normal_model_and_reports(self):
        p,base,data=self.recon('https://example.test:8443/start')
        self.assertEqual(p.returncode,0,p.stdout+p.stderr)
        self.assertIn('https://example.test:8443/start',data['entry_urls'])
        self.assertNotIn('192.0.2.88',data['ips']); self.assertNotIn('10.9.8.7',data['ips'])
        self.assertIn('ns1.example.test',data['axfr']['successful_ns'])
        self.assertTrue(any(r['port']==6379 for r in data['services']))
        self.assertEqual(data['stats']['unauth_high'],0)
        try:
            import jsonschema
        except ImportError:
            pass
        else:
            jsonschema.validate(data,json.loads((ROOT/'references/recon.schema.json').read_text()))
            exported=json.loads((base/'skyshield-import.json').read_text())
            jsonschema.validate(exported,json.loads((ROOT/'references/skyshield-import.schema.json').read_text()))
            self.assertTrue(any(a['type']=='endpoint' and a['value'].endswith('/api/health') and a.get('confidence')=='candidate' for a in exported['assets']))
        cn=(base/'信息收集报告_example.test.md').read_text()
        self.assertIn('6379',cn); self.assertIn('AXFR 成功',cn); self.assertNotIn('${TARGET}',cn)
        calls=self.calls()
        self.assertFalse(any('Authorization: Bearer' in a for c in calls for a in c['args']))
        self.assertFalse(any('vendor.invalid' in a for c in calls for a in c['args']))
        self.assertLessEqual(data['coverage']['cloud']['attempted'],2)
        self.assertEqual(data['cloud_buckets'][0]['source'],'observed_js_html')
    def test_runtime_httpx_failure_falls_back(self):
        p,base,data=self.recon('example.test','--no-port-scan',env=dict(self.env,FAIL_HTTPX='1'))
        self.assertEqual(p.returncode,0,p.stdout+p.stderr)
        self.assertTrue(data['coverage']['http']['fallback']); self.assertGreater(data['stats']['alive'],0)
        self.assertFalse(any(c['tool']=='nmap' for c in self.calls()))
        commands=[json.loads(l) for l in (base/'commands.jsonl').read_text().splitlines()]
        self.assertTrue(any(c['name']=='httpx-alive' and c['status']=='failed' for c in commands))
    def test_broken_httpx_capability_falls_back(self):
        p,base,data=self.recon('example.test','--no-port-scan',env=dict(self.env,BROKEN_HTTPX='1'))
        self.assertEqual(p.returncode,0,p.stdout+p.stderr)
        self.assertFalse(data['tools']['httpx']['usable']); self.assertTrue(data['coverage']['http']['fallback'])
    def test_cidr_host_mapping(self):
        p,base,data=self.recon('203.0.113.99/24')
        self.assertEqual(p.returncode,0,p.stdout+p.stderr)
        self.assertEqual(set(data['ports']),{'203.0.113.1','203.0.113.2'})
        self.assertEqual(data['stats']['ips'],2)
    def test_ipv6_no_port_scan_urls(self):
        p,base,data=self.recon('2001:db8::10','--no-port-scan')
        self.assertEqual(p.returncode,0,p.stdout+p.stderr)
        urls=[r['url'] for r in data['http']]
        self.assertTrue(all('[2001:db8::10]' in u for u in urls))
    def test_stealth_gates_nse_and_validation(self):
        p,base,data=self.recon('example.test','--stealth','--no-port-scan')
        self.assertEqual(p.returncode,0,p.stdout+p.stderr)
        self.assertFalse(any(c['tool']=='nmap' or 'AXFR' in c['args'] for c in self.calls()))
        self.assertEqual(data['steps']['cloud']['status'],'skipped')
    def test_managed_proxy_and_cloud_scope(self):
        env=dict(self.env, RECON_MANAGED_PROXY='1', RECON_HTTP_PROXY='http://127.0.0.1:7777')
        p,base,data=self.recon('example.test','--no-cloud',env=env)
        self.assertEqual(p.returncode,0,p.stdout+p.stderr)
        calls=self.calls()
        for name in ('subfinder','httpx'):
            active=[c for c in calls if c['tool']==name and not set(c['args']).intersection({'-h', '--version', '-version', 'version'})]
            self.assertTrue(active, name)
            self.assertTrue(all('-proxy' in c['args'] and 'http://127.0.0.1:7777' in c['args'] for c in active),name)
        self.assertFalse(any(c['tool']=='nmap' and ('-sV' in c['args'] or any(a.startswith('--script=') for a in c['args'])) for c in calls))
        self.assertEqual(data['steps']['cloud']['status'],'skipped')
        self.assertEqual(data['coverage']['ports']['service_detail'],'disabled_for_http_proxy')
    def test_new_run_does_not_keep_old_ports(self):
        p,base,first=self.recon('203.0.113.10')
        self.assertEqual(p.returncode,0,p.stdout+p.stderr)
        p,_,second=self.recon('203.0.113.10','--fast')
        self.assertEqual(p.returncode,0,p.stdout+p.stderr)
        self.assertNotEqual(first['run_id'],second['run_id']); self.assertEqual(second['services'],[])
        self.assertEqual(len(list((base/'runs').iterdir())),2)
    def test_target_lock(self):
        base=self.work/'reports/example.test'; base.mkdir(parents=True)
        with (base/'.lock').open('a') as lock:
            fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
            p=self.run_cli('recon.sh','example.test','--fast')
        self.assertEqual(p.returncode,3)
    def test_batch_normalizes_and_preserves_seeds(self):
        targets=self.work/'targets.txt'; targets.write_text('example.test\nhttps://EXAMPLE.test:8443/start')
        p=self.run_cli('batch_recon.sh','--targets',targets,'--dry-run')
        self.assertEqual(p.returncode,0,p.stderr)
        jobs=json.loads(p.stdout.split('\n',1)[1])
        self.assertEqual(len(jobs),1); self.assertIn('https://example.test:8443/start',jobs[0]['seeds'])
    def test_batch_execution_status(self):
        p=self.run_cli('batch_recon.sh','example.test','https://EXAMPLE.test/start','--fast','--agents','2')
        self.assertEqual(p.returncode,0,p.stdout+p.stderr)
        state=json.loads(next((self.work/'reports').glob('batch_*.json')).read_text())
        self.assertEqual(len(state['targets']),1); self.assertEqual(state['targets']['example.test']['status'],'completed')
    def test_tool_identity(self):
        p=self.run_cli('check_tools.sh','--require','httpx',env=dict(self.env,BROKEN_HTTPX='1'))
        self.assertEqual(p.returncode,1); self.assertIn('UNAVAILABLE',p.stdout)
    def test_resume_target_mismatch(self):
        state=self.work/'state.json'; state.write_text(json.dumps({'target':'203.0.113.1','scanned':[443],'open':[{'port':443}]}))
        p=subprocess.run([sys.executable,str(ROOT/'scripts/port_scan.py'),'-t','203.0.113.2','--ports','443','--resume',str(state)],capture_output=True,text=True,env=self.env)
        self.assertEqual(p.returncode,2); self.assertIn('another/unknown target',p.stderr)
    def test_invalid_dns_output_falls_back(self):
        p,base,data=self.recon('example.test','--no-port-scan',env=dict(self.env,BAD_DNS_JSON='1'))
        self.assertEqual(p.returncode,0,p.stdout+p.stderr)
        self.assertIn('203.0.113.10',data['ips'])
        self.assertIn('output_error',data['tools']['dnsx'])
    def test_one_failed_api_is_partial(self):
        p,base,data=self.recon('example.test','--no-port-scan',env=dict(self.env,FAIL_API='1'))
        self.assertEqual(p.returncode,2,p.stdout+p.stderr)
        self.assertEqual(data['steps']['web']['status'],'partial')
        summary=json.loads(next((base/'latest/raw').glob('fp-*/web-probe-summary.json')).read_text())
        self.assertEqual(summary['api_failed'],1); self.assertEqual(summary['api_tested'],1)
    def test_short_secret_is_masked(self):
        js=self.work/'assets'; js.mkdir()
        (js/'app.js').write_text('var password = "hunter2";')
        target=self.work/'secrets.json'
        process=subprocess.run([sys.executable,str(ROOT/'scripts/web_secrets.py'),str(js),str(target),str(self.work)],capture_output=True,text=True,env=self.env)
        self.assertEqual(process.returncode,0,process.stderr)
        self.assertNotIn('hunter2',target.read_text())
        self.assertTrue(json.loads(target.read_text())['findings'])
    def test_legacy_migration_does_not_reenter_fast_report(self):
        base=self.work/'reports/example.test'; base.mkdir(parents=True)
        (base/'04-alive.txt').write_text('https://old.example.test [200]')
        (base/'04-ips.txt').write_text('198.51.100.99')
        (base/'axfr.json').write_text('{"status":"success","successful_ns":["old-ns"]}')
        p,base,data=self.recon('example.test','--fast')
        self.assertEqual(p.returncode,0,p.stdout+p.stderr)
        p=self.run_cli('build_report.sh','example.test')
        self.assertEqual(p.returncode,0,p.stderr)
        data=json.loads((base/'recon.json').read_text())
        self.assertEqual(data['alive'],[]); self.assertEqual(data['ips'],[])
        self.assertEqual(data['axfr']['successful_ns'],[])
        self.assertTrue((base/'recon.json').is_symlink())
        self.assertTrue(list((base/'runs').glob('legacy-*/04-alive.txt')))
    def test_batch_cancel_records_interruption(self):
        pidfile=self.work/'nested.pid'
        env=dict(self.env,MOCK_SOURCE_SLEEP='1',MOCK_CHILD_PID=str(pidfile))
        process=subprocess.Popen(['/bin/bash',str(ROOT/'scripts/batch_recon.sh'),'example.test','other.test','--fast','--agents','1'],cwd=self.work,env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,start_new_session=True)
        try:
            deadline=time.monotonic()+8
            while not pidfile.exists() and time.monotonic()<deadline: time.sleep(.05)
            self.assertTrue(pidfile.exists())
            process.send_signal(signal.SIGINT)
            stdout,stderr=process.communicate(timeout=15)
            self.assertEqual(process.returncode,130,stdout+stderr)
            state=json.loads(next((self.work/'reports').glob('batch_*.json')).read_text())
            self.assertEqual(state['status'],'interrupted')
            self.assertNotIn('running',[v['status'] for v in state['targets'].values()])
            self.assertTrue(list((self.work/'reports').glob('批量侦察汇总_*.md')))
            manifest=json.loads(next((self.work/'reports/example.test/runs').glob('*/manifest.json')).read_text())
            self.assertEqual(manifest['status'],'interrupted')
        finally:
            if process.poll() is None:
                from recon_common import terminate_tree
                terminate_tree(process); process.wait()
    def test_command_timeout(self):
        out=self.work/'timeout'; out.mkdir()
        r=run_command([sys.executable,'-c','import time; time.sleep(30)'],out,'timeout',timeout=.1)
        self.assertEqual(r['status'],'timeout'); self.assertEqual(r['returncode'],124)

    def test_timeout_without_proc_stops_nested_supervised_command(self):
        out=self.work/'restricted-timeout'; out.mkdir()
        pidfile=self.work/'grandchild.pid'
        nested=self.work/'nested.py'
        payload=f'import os,time; from pathlib import Path; Path({str(pidfile)!r}).write_text(str(os.getpid())); time.sleep(30)'
        nested.write_text(f'import sys; sys.path.insert(0,{str(ROOT / "scripts")!r}); from recon_common import run_command; run_command([sys.executable,"-c",{payload!r}],{str(out)!r},"inner",timeout=25)')
        original=Path.iterdir
        def restricted(path):
            if path == Path('/proc'): raise PermissionError('sandbox denied /proc')
            return original(path)
        with patch.object(Path, 'iterdir', restricted):
            result=run_command([sys.executable,str(nested)],out,'outer',timeout=1.5)
        self.assertEqual(result['status'],'timeout'); self.assertEqual(result['returncode'],124)
        self.assertTrue(pidfile.exists(), 'nested command did not start')
        pid=int(pidfile.read_text())
        deadline=time.monotonic()+3
        while time.monotonic()<deadline:
            try: state=Path(f'/proc/{pid}/stat').read_text().rsplit(')',1)[1].split()[0]
            except (OSError, IndexError): break
            if state == 'Z': break
            time.sleep(.05)
        else: self.fail('nested command kept running after its parent timed out')


class LegacyReportTests(unittest.TestCase):
    def test_corrupt_evidence_is_partial(self):
        with tempfile.TemporaryDirectory() as tmp:
            out=Path(tmp)/'example.test'; (out/'raw').mkdir(parents=True)
            (out/'manifest.json').write_text('{"target":"example.test","target_id":"example.test","status":"completed"}')
            for name in ('http-records.json','axfr.json','raw/cloud-buckets.json','raw/org-assets.json'):
                (out/name).write_text('{broken json')
            data=build_report(out)
            self.assertEqual(data['status'],'partial'); self.assertEqual(len(data['evidence_errors']),4)
    def test_fallback_ports_and_mixed_axfr(self):
        with tempfile.TemporaryDirectory() as tmp:
            out=Path(tmp)/'example.test'; raw=out/'raw'; raw.mkdir(parents=True)
            (out/'00-scope.md').write_text('- Normalized target: example.test\n')
            (raw/'tcp-203.0.113.10.json').write_text(json.dumps({'target':'203.0.113.10','open':[{'port':6379}]}))
            (out/'02-axfr.txt').write_text('=== AXFR ns1.example.test ===\nexample.test. 60 IN SOA ns1.example.test. admin.example.test. 1 2 3 4 5\napp.example.test. 60 IN A 203.0.113.10\n=== AXFR ns2.example.test ===\n; Transfer failed.\n')
            fp=raw/'fp-example.test/raw'; fp.mkdir(parents=True)
            (fp/'nuclei-tech.txt').write_text('fixture\n'); (fp/'path-probe.txt').write_text('200 https://example.test/.git/HEAD\n')
            data=build_report(out)
            self.assertEqual(data['stats']['nuclei_tech'],1); self.assertEqual(data['stats']['open_services_total'],1)
            self.assertIn('ns1.example.test',data['axfr']['successful_ns'])
            cn=(out/'信息收集报告_example.test.md').read_text()
            self.assertIn('6379',cn); self.assertIn('.git/HEAD',cn); self.assertIn('unknown',cn)


if __name__=='__main__': unittest.main()
