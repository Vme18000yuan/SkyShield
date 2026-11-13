"""Offline checks for phase-by-phase execution and evidence checkpoints."""
from __future__ import annotations

import contextlib
import io
import json
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'scripts'))
import agentic_recon
from recon_common import atomic_json, write_lines
from recon_pipeline import Pipeline


class AgenticReconTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.args = SimpleNamespace(target='example.test', mode='normal', stage='passive',
            run_id='test-run', output_dir=self.root, max_web_hosts=5, stage_timeout=30)
        self.out = self.root / 'example.test' / 'runs' / 'test-run'

    def execute(self, stage):
        self.args.stage = stage
        with contextlib.redirect_stdout(io.StringIO()):
            return agentic_recon.execute(self.args)

    def test_phase_results_checkpoint_and_finish_omissions(self):
        def passive(pipeline):
            write_lines(pipeline.out / '01-subdomains.txt', ['example.test', 'app.example.test'])
            return {'status': 'success'}

        def dns(pipeline):
            atomic_json(pipeline.out / 'dns-records.json', [
                {'host': 'app.example.test', 'type': 'A', 'ip': '192.0.2.10'}])
            write_lines(pipeline.out / '04-ips.txt', ['192.0.2.10'])
            return {'status': 'success', 'records': 1}

        def http(pipeline):
            atomic_json(pipeline.out / 'http-records.json', [
                {'url': 'https://app.example.test/', 'status_code': 200}])
            return {'status': 'success'}

        def web(pipeline):
            self.assertEqual(pipeline.a.max_web_hosts, 8)
            pipeline.m['coverage']['web'] = {'selected': 1, 'omitted': 0, 'completed': 1}
            return {'status': 'success'}

        with patch.object(Pipeline, 'passive', passive), patch.object(Pipeline, 'dns', dns), \
                patch.object(Pipeline, 'http', http), patch.object(Pipeline, 'web', web):
            self.assertEqual(self.execute('passive'), 0)
            first = json.loads((self.out / 'skyshield-import.json').read_text())
            self.assertEqual(first['run']['status'], 'partial')
            self.assertEqual(json.loads((self.out / 'manifest.json').read_text())['status'], 'running')
            self.assertEqual(self.execute('dns'), 0)
            second = json.loads((self.out / 'skyshield-import.json').read_text())
            ip = next(item for item in second['assets'] if item['type'] == 'ip')
            self.assertEqual(ip['bound_domains'], ['app.example.test'])
            self.assertEqual(self.execute('http'), 0)
            self.args.max_web_hosts = 8
            self.assertEqual(self.execute('web'), 0)
            self.assertEqual(self.execute('finish'), 0)
        final = json.loads((self.out / 'manifest.json').read_text())
        self.assertEqual(final['status'], 'partial')
        self.assertEqual(final['steps']['dns']['status'], 'success')
        self.assertEqual(final['coverage']['web']['completed'], 1)
        self.assertIn('ports', final['coverage']['agentic']['unexecuted'])
        self.assertTrue((self.root / 'example.test' / 'latest').is_symlink())
        with self.assertRaisesRegex(ValueError, '无法继续'):
            self.execute('dns')

        self.args.resume = True
        with patch.object(Pipeline, 'dns', dns):
            self.assertEqual(self.execute('dns'), 0)
            resumed = json.loads((self.out / 'manifest.json').read_text())
            self.assertEqual(resumed['status'], 'running')
            self.assertEqual(resumed['steps']['http']['status'], 'success')
            self.assertNotIn('agentic', resumed['coverage'])
            self.assertEqual(self.execute('finish'), 0)

    def test_retry_limit_and_manifest_scope_are_enforced(self):
        def partial(_pipeline):
            return {'status': 'partial'}
        with patch.object(Pipeline, 'dns', partial):
            self.assertEqual(self.execute('dns'), 0)
            self.assertEqual(self.execute('dns'), 0)
            with self.assertRaisesRegex(ValueError, '重试上限'):
                self.execute('dns')
        manifest = json.loads((self.out / 'manifest.json').read_text())
        manifest['input'] = 'other.example.test'
        atomic_json(self.out / 'manifest.json', manifest)
        with self.assertRaisesRegex(ValueError, '原始目标不匹配'):
            self.execute('http')

    def test_fast_mode_cannot_execute_active_phases(self):
        self.args.mode = 'fast'
        with self.assertRaisesRegex(ValueError, '不适用于'):
            self.execute('ports')
        with patch.object(Pipeline, 'passive', lambda _: {'status': 'success'}):
            self.assertEqual(self.execute('passive'), 0)
            self.assertEqual(self.execute('finish'), 0)
        manifest = json.loads((self.out / 'manifest.json').read_text())
        self.assertEqual(manifest['status'], 'partial')
        self.assertEqual(manifest['coverage']['agentic']['unexecuted'], ['organization'])


if __name__ == '__main__':
    unittest.main()
