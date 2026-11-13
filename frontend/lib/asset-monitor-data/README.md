# Offline city-name data

`cities.zh.json` is a locally bundled gazetteer for the asset-monitor map. Rows
are `[countryCode, latitude, longitude, simplifiedChineseName, englishNames]`.
It contains a modified selection of GeoNames cities1000 and alternate names,
supplemented by Natural Earth populated places. The GeoNames data is licensed
under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/); attribution is
shown on the map. The modifications filter to modern Chinese place names and exclude GeoNames
neighborhood, abandoned and historical feature types,
normalize them to Simplified Chinese with OpenCC, and combine same-country,
same-name entries that have nearby coordinates. Natural Earth data is in the
[public domain](https://www.naturalearthdata.com/about/terms-of-use/).

Source snapshots and SHA-256 hashes:

- [GeoNames cities1000.zip](https://download.geonames.org/export/dump/cities1000.zip), 2026-11-13: `52a3cc91111ad6526e7331fcd4c0767b10be0c4740907a828448c1517487448e`
- [GeoNames alternateNamesV2.zip](https://download.geonames.org/export/dump/alternateNamesV2.zip), 2026-11-13: `0d072a4d20e34741302874039df3d2afdbe6786c5a3c9ffe32aa049615c7b2b3`
- [Natural Earth 10m populated places v5.1.2](https://github.com/nvkelso/natural-earth-vector/blob/v5.1.2/geojson/ne_10m_populated_places.geojson): `9b8e3de09048ef00dfc70357dbb9fa324493f214b5e0ae4daf1aa79a8d10116b`

The GeoNames dump URLs are rolling; retain exact input snapshots to reproduce.
Run `python3 scripts/build-monitor-cities.py <cities.zip> <alternates.zip> <natural-earth.geojson>`
after installing frontend dependencies. The script validates all inputs and
never reads or sends asset data. OpenCC-JS licenses accompany the distribution in
`public/licenses/opencc-js`.
