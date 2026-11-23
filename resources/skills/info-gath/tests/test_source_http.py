"""Exercise source retries with real curl against a loopback HTTP fixture."""
import http.server
import json
import os
from pathlib import Path
import shutil
import sys
import tempfile
import threading
import time
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'scripts'))
from source_http import fetch_source


@unittest.skipUnless(shutil.which('curl'), 'curl not installed')
class SourceHTTPTests(unittest.TestCase):
    def setUp(self):
        self.responses = [502, 200]
        self.requests = []
        fixture = self
        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                fixture.requests.append(self.path)
                code = fixture.responses.pop(0) if len(fixture.responses) > 1 else fixture.responses[0]
                body = b'[{"name_value":"app.example.test"}]' if code == 200 else b'error'
                self.send_response(code)
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            def log_message(self, *args): pass
        self.server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.temp = tempfile.TemporaryDirectory()
        self.out = Path(self.temp.name)
        self.url = f'http://127.0.0.1:{self.server.server_port}/data'

    def tearDown(self):
        self.server.shutdown(); self.server.server_close(); self.thread.join()
        self.temp.cleanup()

    def fetch(self, seconds=10, url=None):
        return fetch_source(url or self.url, self.out, 'source-crtsh', stdout=self.out / 'data.json',
            deadline=time.monotonic() + seconds, max_bytes=1024)

    def test_transient_failure_recovers_with_valid_output(self):
        with patch.dict(os.environ, NO_PROXY='127.0.0.1', no_proxy='127.0.0.1'):
            result = self.fetch()
        self.assertEqual(result['status'], 'success')
        self.assertEqual(result['attempts'], 2)
        self.assertNotIn('http_status', result)
        self.assertEqual(json.loads((self.out / 'data.json').read_text())[0]['name_value'], 'app.example.test')
        records = [json.loads(line) for line in (self.out / 'commands.jsonl').read_text().splitlines()]
        self.assertEqual([record['returncode'] for record in records], [22, 0])

    def test_persistent_failure_keeps_real_error_after_three_attempts(self):
        self.responses = [502]
        with patch.dict(os.environ, NO_PROXY='127.0.0.1', no_proxy='127.0.0.1'):
            result = self.fetch()
        self.assertEqual((result['status'], result['http_status'], result['attempts']), ('failed', 502, 3))
        self.assertEqual(len(self.requests), 3)
        self.assertEqual((self.out / 'data.json').read_bytes(), b'')

    def test_forbidden_is_not_retried(self):
        self.responses = [403]
        with patch.dict(os.environ, NO_PROXY='127.0.0.1', no_proxy='127.0.0.1'):
            result = self.fetch()
        self.assertEqual((result['http_status'], result['attempts']), (403, 1))

    def test_phase_deadline_stops_retries(self):
        self.responses = [502]
        with patch.dict(os.environ, NO_PROXY='127.0.0.1', no_proxy='127.0.0.1'):
            result = self.fetch(seconds=.5)
            expired = self.fetch(seconds=-1)
        self.assertEqual(result['attempts'], 1)
        self.assertEqual(expired['attempts'], 0)
        self.assertEqual(expired['status'], 'timeout')
        self.assertEqual(len(self.requests), 1)

    def test_configured_proxy_is_used_without_direct_fallback(self):
        self.responses = [200]
        proxy = f'http://127.0.0.1:{self.server.server_port}'
        with patch.dict(os.environ, HTTP_PROXY=proxy, http_proxy=proxy, NO_PROXY='', no_proxy=''):
            result = self.fetch(url='http://source.invalid/data')
        self.assertEqual(result['status'], 'success')
        self.assertEqual(self.requests, ['http://source.invalid/data'])


if __name__ == '__main__':
    unittest.main()
