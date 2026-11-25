"""Offline port checkpoints: reuse completed hosts without losing evidence."""
import contextlib
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'scripts'))
import scan_ports


class PortResumeTests(unittest.TestCase):
    def test_only_unfinished_targets_are_retried_with_a_different_budget(self):
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory)
            (out / 'raw').mkdir()
            calls = []

            def run(argv, *_args, **_kwargs):
                targets = Path(argv[argv.index('-iL') + 1]).read_text().splitlines()
                calls.append((targets, argv))
                hosts = []
                for target in targets:
                    timeout = len(calls) == 1 and target.endswith('.2')
                    hosts.append(f'<host timedout="{str(timeout).lower()}"><address addr="{target}" addrtype="ipv4"/><ports><port protocol="tcp" portid="443"><state state="open"/></port></ports></host>')
                Path(argv[argv.index('-oX') + 1]).write_text('<nmaprun>' + ''.join(hosts) + '</nmaprun>')
                return {'status': 'success'}

            targets = ['192.0.2.1', '192.0.2.2']
            with patch.object(scan_ports, 'run_command', run), contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(scan_ports.scan_normal_nmap(targets, out, 2000, 300), 2)
                self.assertEqual(scan_ports.scan_normal_nmap(targets, out, 2000, 300), 0)
                self.assertEqual(scan_ports.scan_normal_nmap(targets, out, 2000, 300), 0)
            self.assertEqual(len(calls), 2)
            self.assertEqual(calls[1][0], ['192.0.2.2'])
            self.assertEqual(calls[0][1][calls[0][1].index('--host-timeout') + 1], '60s')
            self.assertEqual(calls[1][1][calls[1][1].index('--host-timeout') + 1], '120s')
            self.assertEqual(calls[1][1][calls[1][1].index('--max-rate') + 1], '500')
            evidence = json.loads((out / 'raw/ports.json').read_text())
            self.assertEqual({item['ip'] for item in evidence}, set(targets))
            self.assertEqual(len(evidence), 2)
            state = json.loads((out / 'raw/port-scan-status.json').read_text())
            self.assertEqual(state['completed_targets'], 2)
            self.assertEqual(state['reused_targets'], 2)

    def test_failed_retry_preserves_prior_findings_and_scope(self):
        with tempfile.TemporaryDirectory() as directory:
            out = Path(directory)
            (out / 'raw').mkdir()
            (out / 'raw/ports.json').write_text(json.dumps([
                {'ip': '192.0.2.1', 'port': 80, 'protocol': 'tcp'},
                {'ip': '192.0.2.99', 'port': 80, 'protocol': 'tcp'},
            ]))
            (out / 'raw/port-scan-status.json').write_text(json.dumps({
                'scan_profile': 'normal-tcp-top100-v1', 'targets': ['192.0.2.1', '192.0.2.2'],
                'results': [{'target': '192.0.2.1', 'status': 'success'}, {'target': '192.0.2.2', 'status': 'timeout'}],
            }))
            with patch.object(scan_ports, 'run_command', return_value={'status': 'timeout'}), contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(scan_ports.scan_normal_nmap(['192.0.2.1', '192.0.2.2'], out, 2000, 300), 2)
            self.assertEqual([item['ip'] for item in json.loads((out / 'raw/ports.json').read_text())], ['192.0.2.1'])
            state = json.loads((out / 'raw/port-scan-status.json').read_text())
            self.assertEqual(state['completed_targets'], 1)
            self.assertEqual(state['unprocessed_targets'], 1)


if __name__ == '__main__':
    unittest.main()
