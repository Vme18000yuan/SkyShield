#!/usr/bin/env python3
"""Build the offline monitor gazetteer from pinned public source files.

Usage: python3 scripts/build-monitor-cities.py CITIES_ZIP ALTERNATES_ZIP NATURAL_EARTH_GEOJSON
The archives are read locally; asset data is never read or uploaded.
"""
import hashlib
import json
import math
import re
import subprocess
import sys
import tempfile
import unicodedata
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
EXPECTED = (
    '52a3cc91111ad6526e7331fcd4c0767b10be0c4740907a828448c1517487448e',
    '0d072a4d20e34741302874039df3d2afdbe6786c5a3c9ffe32aa049615c7b2b3',
    '9b8e3de09048ef00dfc70357dbb9fa324493f214b5e0ae4daf1aa79a8d10116b',
)
if len(sys.argv) != 4:
    sys.exit(__doc__)
paths = [Path(p) for p in sys.argv[1:]]
for path, expected in zip(paths, EXPECTED):
    digest = hashlib.file_digest(path.open('rb'), 'sha256').hexdigest()
    if digest != expected:
        sys.exit(f'Unexpected checksum for {path}: {digest}')

# GeoNames cities1000 fields: id, name, ascii, alternate names, lat, lon,
# feature class/code, country, ... . Keep tagged names rather than guessing
# which item in its untagged multilingual alias column is modern Chinese.
# Exclude PPLX (neighborhoods), abandoned and historical places from city hints.
places = {}
with zipfile.ZipFile(paths[0]) as archive, archive.open('cities1000.txt') as raw:
    for line in raw:
        parts = line.decode('utf-8').rstrip('\n').split('\t')
        if len(parts) >= 15 and parts[6] == 'P' and parts[7] in {'PPL', 'PPLA', 'PPLA2', 'PPLA3', 'PPLA4', 'PPLA5', 'PPLC'} and re.fullmatch(r'[A-Z]{2}', parts[8]):
            places[parts[0]] = {'cc': parts[8], 'lat': float(parts[4]), 'lon': float(parts[5]),
                                'names': list(dict.fromkeys(parts[1:3])), 'zh': None, 'score': None}

langs = {'zh-CN': 0, 'zh-Hans': 0, 'zh': 1, 'zh-SG': 2, 'zh-TW': 3, 'zh-Hant': 3, 'zh-HK': 4}
with zipfile.ZipFile(paths[1]) as archive, archive.open('alternateNamesV2.txt') as raw:
    for line in raw:
        fields = line.decode('utf-8').rstrip('\n').split('\t')
        if len(fields) < 8 or fields[1] not in places or fields[7] == '1' or fields[6] == '1':
            continue
        place = places[fields[1]]
        lang, name = fields[2], fields[3].strip()
        if lang in langs and re.search(r'[\u3400-\u9fff]', name) and not re.search(r'[A-Za-z0-9]', name):
            score = (langs[lang], 0 if fields[4] == '1' else 1, 0 if fields[5] == '1' else 1, len(name), name)
            if place['score'] is None or score < place['score']:
                place['zh'], place['score'] = name, score
        elif lang == 'en' and name and len(place['names']) < 8 and name not in place['names']:
            place['names'].append(name)

places = [p for p in places.values() if p['zh']]
def normalize(name):
    return ''.join(c for c in unicodedata.normalize('NFKD', name).casefold() if c.isascii() and c.isalnum())

# Merge Natural Earth labels only when country, a name and coordinates agree;
# never merge homonyms from different countries or distant places.
index = {}
for p in places:
    for name in p['names']:
        if key := normalize(name):
            index.setdefault((p['cc'], key), []).append(p)
for feature in json.loads(paths[2].read_text())['features']:
    p = feature['properties']
    cc, zh = p.get('ISO_A2'), p.get('NAME_ZH')
    if not isinstance(cc, str) or not re.fullmatch(r'[A-Z]{2}', cc) or not zh or not re.search(r'[\u3400-\u9fff]', zh):
        continue
    lat, lon = p.get('LATITUDE'), p.get('LONGITUDE')
    if lat is None or lon is None:
        continue
    names = list(dict.fromkeys(n for n in (p.get('NAME'), p.get('NAME_EN'), p.get('NAMEASCII')) if n))
    matches = []
    for name in names:
        for candidate in index.get((cc, normalize(name)), []):
            distance = math.hypot((candidate['lat'] - lat) * 111, (candidate['lon'] - lon) * 111 * math.cos(math.radians(lat)))
            if distance <= 10:
                matches.append((distance, candidate))
    if matches:
        target = min(matches, key=lambda x: x[0])[1]
        target['zh'] = zh
        target['names'] = list(dict.fromkeys(target['names'] + names))
    else:
        places.append({'cc': cc, 'lat': lat, 'lon': lon, 'zh': zh, 'names': names})

rows = [[p['cc'], round(p['lat'], 5), round(p['lon'], 5), p['zh'], p['names']] for p in places]
rows.sort(key=lambda row: (row[0], row[1], row[2], row[3]))
with tempfile.NamedTemporaryFile('w', encoding='utf-8', suffix='.json') as temp:
    json.dump(rows, temp, ensure_ascii=False, separators=(',', ':'))
    temp.flush()
    subprocess.run(['node', str(ROOT / 'scripts/build-monitor-cities-normalize.mjs'), temp.name, str(ROOT / 'lib/asset-monitor-data/cities.zh.json')], check=True, cwd=ROOT)
