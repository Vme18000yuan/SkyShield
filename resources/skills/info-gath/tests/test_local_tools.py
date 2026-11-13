"""Real curl/nmap smoke checks against an ephemeral loopback HTTP service only."""
import http.server
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest.mock import patch

sys.path.insert(0,str(Path(__file__).resolve().parents[1]/'scripts'))
from recon_common import parse_nmap_xml
from web_analysis import HttpClient


class LoopbackTools(unittest.TestCase):
    def setUp(self):
        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                body=b'{"status":"ok"}'
                self.send_response(200)
                self.send_header('Content-Type','application/json')
                self.send_header('Content-Length',str(len(body)))
                self.end_headers(); self.wfile.write(body)
            def handle(self):
                try: super().handle()
                except ConnectionResetError: pass
            def log_message(self,*args): pass
        self.server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler)
        self.thread=threading.Thread(target=self.server.serve_forever,daemon=True);self.thread.start()
        self.temp=tempfile.TemporaryDirectory(prefix='recon-loopback-')
    def tearDown(self):
        self.server.shutdown();self.server.server_close();self.thread.join();self.temp.cleanup()
    @unittest.skipUnless(shutil.which('curl'),'curl not installed')
    def test_curl_response_metadata_and_evidence(self):
        client=HttpClient(Path(self.temp.name),'127.0.0.1',rate=100,seconds=5)
        with patch.dict(os.environ,NO_PROXY='127.0.0.1',no_proxy='127.0.0.1'):
            response=client.fetch(f'http://127.0.0.1:{self.server.server_port}/api/health')
        self.assertEqual(response.code,200)
        self.assertIn('json',response.ctype)
        self.assertEqual(response.body,b'{"status":"ok"}')
        self.assertTrue((Path(self.temp.name)/response.evidence).is_file())
    @unittest.skipUnless(shutil.which('curl'),'curl not installed')
    def test_curl_uses_proxy_for_loopback_target(self):
        observed=[]
        class Proxy(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                observed.append(self.path)
                body=b'{"via":"proxy"}'
                self.send_response(200);self.send_header('Content-Type','application/json')
                self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
            def log_message(self,*args): pass
        proxy=http.server.ThreadingHTTPServer(('127.0.0.1',0),Proxy)
        thread=threading.Thread(target=proxy.serve_forever,daemon=True);thread.start()
        try:
            address=f'http://127.0.0.1:{proxy.server_port}'
            env=dict(http_proxy=address,https_proxy=address,HTTP_PROXY=address,HTTPS_PROXY=address,NO_PROXY='',no_proxy='')
            with patch.dict(os.environ,env):
                response=HttpClient(Path(self.temp.name),'127.0.0.1',rate=100,seconds=5).fetch(f'http://127.0.0.1:{self.server.server_port}/via-proxy')
            self.assertEqual(response.body,b'{"via":"proxy"}')
            self.assertEqual(observed,[f'http://127.0.0.1:{self.server.server_port}/via-proxy'])
        finally:
            proxy.shutdown();proxy.server_close();thread.join()
    @unittest.skipUnless(shutil.which('nmap'),'nmap not installed')
    def test_nmap_xml_real_open_host(self):
        path=Path(self.temp.name)/'nmap.xml'
        p=subprocess.run(['nmap','-n','-Pn','-sT','-p',str(self.server.server_port),'127.0.0.1','-oX',str(path)],capture_output=True,text=True,timeout=15)
        self.assertEqual(p.returncode,0,p.stderr)
        records=parse_nmap_xml(path)
        self.assertEqual([(r['ip'],r['port']) for r in records],[('127.0.0.1',self.server.server_port)])
