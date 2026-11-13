"""The product import contract is deterministic and never promotes clues to findings."""
import copy
import json
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'scripts'))
from skyshield_output import skyshield_output


class SkyShieldOutputTests(unittest.TestCase):
    def sample(self):
        return dict(target='app.example.test', run_id='test-run', status='partial', mode='normal',
            subdomains=['app.example.test', 'x.app.example.test', 'other.example.test'],
            ips=['203.0.113.1', '203.0.113.2', '203.0.113.3'],
            dns_records=[{'host':'app.example.test','type':'A','ip':'203.0.113.1'},
                         {'host':'x.app.example.test','type':'A','ip':'203.0.113.2'},
                         {'host':'other.example.test','type':'A','ip':'203.0.113.3'}],
            cdn_excluded=[{'ip':'203.0.113.2'}],
            http=[{'url':'https://app.example.test/api?token=topsecret', 'status_code':200,
                   'title':'Console token=topsecret', 'tech':['nginx','PHP:8.3']}],
            services=[{'ip':'203.0.113.1','port':22,'protocol':'tcp','service':'ssh','evidence':'raw/ports.json'},
                      {'ip':'203.0.113.2','port':443,'protocol':'tcp','service':'https','evidence':'../secrets'}],
            org_assets={'related_roots':['unrelated.test']},
            unauth_findings=[{'url':'https://app.example.test/api?password=topsecret','verdict':'ANONYMOUS_DATA'}],
            cloud_buckets=[{'url':'https://bucket.vendor.test/?key=topsecret','verdict':'PUBLIC_LISTING'}],
            axfr={'status':'success'})

    def test_conservative_scope_and_redaction(self):
        out = skyshield_output(self.sample())
        self.assertEqual(out['run']['status'], 'partial')
        assets = {(a['type'], a['value']):a for a in out['assets']}
        self.assertEqual(assets['domain','other.example.test']['scope'], 'out_of_scope')
        self.assertEqual(assets['domain','unrelated.test']['scope'], 'out_of_scope')
        self.assertEqual(assets['ip','203.0.113.1']['scope'], 'associated')
        self.assertEqual(assets['ip','203.0.113.2']['scope'], 'unverified')
        self.assertEqual(assets['ip','203.0.113.3']['scope'], 'unverified')
        self.assertEqual(assets['ip','203.0.113.1']['bound_domains'], ['app.example.test'])
        service = next(a for a in out['assets'] if a.get('port') == 443)
        self.assertEqual(service['scope'], 'unverified')
        self.assertEqual(service['evidence'], [])
        self.assertNotIn('topsecret', json.dumps(out))
        self.assertEqual(assets['service','https://app.example.test/api']['technologies'], ['PHP:8.3', 'nginx'])
        self.assertIn('[REDACTED]', assets['service','https://app.example.test/api']['page_title'])
        self.assertEqual(len(out['vulnerabilities']), 1)
        self.assertEqual(out['vulnerabilities'][0]['status'], 'needs_review')
        self.assertEqual(out['vulnerabilities'][0]['confidence'], 'candidate')
        self.assertEqual(out['vulnerabilities'][0]['severity'], 'info')
        self.assertTrue(all(o['status'] == 'needs_review' for o in out['observations']))
        self.assertEqual({o['category'] for o in out['observations']}, {'ANONYMOUS_DATA','PUBLIC_LISTING','DNS_ZONE_TRANSFER'})

    def test_cidr_and_ipv6(self):
        data = dict(target='2001:db8::/120', ips=['2001:db8::10','2001:db8::100'], services=[], http=[], subdomains=[])
        assets = {a['value']:a for a in skyshield_output(data)['assets']}
        self.assertEqual(assets['2001:db8::10']['scope'], 'in_scope')
        self.assertEqual(assets['2001:db8::100']['scope'], 'out_of_scope')

    def test_stable_ids_ignore_order_and_duplicate_dns(self):
        data = self.sample()
        out = skyshield_output(data)
        data['subdomains'].reverse()
        data['dns_records'].append(copy.copy(data['dns_records'][0]))
        data['services'].reverse()
        self.assertEqual(out, skyshield_output(data))

    def test_bad_data_is_not_promoted(self):
        data = self.sample()
        data['http'].extend([{'url':'https://u:secret@app.example.test/'}, {'url':'javascript:alert(1)'}])
        data['services'].append({'ip':'bad','port':22,'protocol':'tcp'})
        out = skyshield_output(data)
        self.assertNotIn('secret', json.dumps(out))
        self.assertEqual(len([a for a in out['assets'] if a['type'] == 'service']), 3)


if __name__ == '__main__':
    unittest.main()
