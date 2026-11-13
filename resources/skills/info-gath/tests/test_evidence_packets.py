"""Evidence captures are local fixtures; no target traffic is sent."""
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'scripts'))
from web_analysis import HttpClient
from skyshield_output import skyshield_output


class EvidencePacketTests(unittest.TestCase):
    def test_captures_outgoing_headers_and_body_after_proxy_connect(self):
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory)
            def run(args, root, name, **kwargs):
                body = Path(args[args.index('-o') + 1])
                body.write_text('{"ok":true}')
                Path(args[args.index('--dump-header') + 1]).write_text('HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n')
                Path(kwargs['stdout']).write_text('200|application/json')
                (root / 'raw' / (name + '.stderr')).write_text('> CONNECT example.test:443 HTTP/1.1\n> Host: example.test:443\n> \n* TLS ready\n> POST /api HTTP/1.1\n> Host: example.test\n> Content-Type: application/json\n> \n')
                return {'status': 'success'}
            with patch('web_analysis.run_command', side_effect=run):
                response = HttpClient(out, 'example.test', rate=10000).fetch('https://example.test/api', payload={'test': 1})
            body = out / response.evidence
            request = body.with_suffix('.request').read_text()
            self.assertTrue(request.startswith('POST /api HTTP/1.1'))
            self.assertNotIn('CONNECT', request)
            self.assertTrue(request.endswith(json.dumps({'test': 1})))
            self.assertIn('200 OK', body.with_suffix('.headers').read_text())

    def test_import_preserves_packet_path_and_stable_type(self):
        report = skyshield_output({'target': 'example.test', 'unauth_findings': [
            {'url': 'https://example.test/api', 'verdict': 'ANONYMOUS_DATA', 'evidence': 'raw/fp-example/raw/http/abc.body', 'token_evidence': '../secret'}]})
        finding = report['vulnerabilities'][0]
        self.assertEqual(finding['evidence'], ['recon.json', 'raw/fp-example/raw/http/abc.body'])
        self.assertEqual(finding['title'], '匿名数据响应')
        self.assertEqual(finding['status'], 'needs_review')


if __name__ == '__main__':
    unittest.main()
