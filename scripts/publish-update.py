#!/usr/bin/env python3
"""Generate the stable update channel only after all immutable images exist.

Run after publishing the four sha-<revision[:12]> image tags, then commit the
resulting updates/stable.json. No application or container is started here.
"""
import argparse
import datetime
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
ACCEPT = ', '.join(('application/vnd.oci.image.index.v1+json', 'application/vnd.docker.distribution.manifest.list.v2+json', 'application/vnd.oci.image.manifest.v1+json', 'application/vnd.docker.distribution.manifest.v2+json'))


def fetch(url, token=None):
    headers = {'Accept': ACCEPT}
    if token:headers['Authorization'] = 'Bearer ' + token
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=30) as response:
        data = response.read(4*1024*1024+1)
    if len(data)>4*1024*1024:raise RuntimeError('Registry metadata too large')
    return data


def main():
    parser=argparse.ArgumentParser();parser.add_argument('--revision',required=True);parser.add_argument('--version',required=True);parser.add_argument('--notes',type=Path,required=True)
    args=parser.parse_args()
    spec=importlib.util.spec_from_file_location('skyshield_update',ROOT/'scripts/system-update.py');updater=importlib.util.module_from_spec(spec);spec.loader.exec_module(updater)
    revision=subprocess.check_output(['git','rev-parse',args.revision+'^{commit}'],cwd=ROOT,text=True).strip()
    tag='sha-'+revision[:12];images={}
    for name in updater.COMPONENTS:
        repo='solder110/skyshield-'+name
        token=json.loads(fetch('https://auth.docker.io/token?'+urllib.parse.urlencode({'service':'registry.docker.io','scope':'repository:'+repo+':pull'})))['token']
        base='https://registry-1.docker.io/v2/'+repo;raw=fetch(base+'/manifests/'+tag,token);digest='sha256:'+hashlib.sha256(raw).hexdigest();manifest=json.loads(raw)
        if 'manifests' in manifest:
            matches=[m for m in manifest['manifests'] if m.get('platform',{}).get('architecture')=='amd64' and m.get('platform',{}).get('os')=='linux']
            if len(matches)!=1:raise RuntimeError('Expected one linux/amd64 image: '+name)
            child=fetch(base+'/manifests/'+matches[0]['digest'],token)
            if 'sha256:'+hashlib.sha256(child).hexdigest()!=matches[0]['digest']:raise RuntimeError('Manifest digest mismatch')
            manifest=json.loads(child)
        raw_config=fetch(base+'/blobs/'+manifest['config']['digest'],token)
        if 'sha256:'+hashlib.sha256(raw_config).hexdigest()!=manifest['config']['digest']:raise RuntimeError('Image config digest mismatch')
        config=json.loads(raw_config)
        if config.get('architecture')!='amd64' or config.get('os')!='linux' or config.get('config',{}).get('Labels',{}).get('org.opencontainers.image.revision')!=revision:
            raise RuntimeError('Image does not match the published source: '+name)
        images[name]='docker.io/'+repo+'@'+digest
    release=updater.validate_manifest({'schemaVersion':1,'minimumUpdaterVersion':1,'version':args.version,'revision':revision,'publishedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'notes':args.notes.read_text(),'images':images})
    destination=ROOT/'updates/stable.json';destination.parent.mkdir(exist_ok=True)
    destination.write_text(json.dumps(release,ensure_ascii=False,indent=2)+'\n')
    print('Prepared '+str(destination.relative_to(ROOT))+'. Commit and push it to announce this release.')


if __name__=='__main__':main()
