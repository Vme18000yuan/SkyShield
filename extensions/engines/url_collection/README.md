# URL Collection Engine

`engine.lunafox.url_collection` is the first-party URL Collection Engine. It
uses the Server-produced `websiteURLs` role at runtime through
`Execution.Input.WebsiteURLs.Path(ctx)` and supports domain, IP, and CIDR
Targets. The generated contract still exposes the complete closed input Registry;
this Engine does not declare an input allow-set.

The Server prepares `websiteURLs` as finalized Website URL facts from the
current scan. It does not inject Target baseline URLs. The Engine constructs a
Target-baseline-first workspace seed, appends snapshot URLs in their stored
order, and keeps the existing overlap rules. A successful zero-byte facts file is
valid as long as Target baseline generation succeeds.

The seed materializer streams finalized Website URL lines directly to the
task-local Katana artifact and reports a checked `uint64` count. Target/CIDR
baselines are emitted one address at a time; no URL, byte, record, or CIDR
business cap is applied and no prefix is silently discarded. Framing, write,
close, cancellation, and counter-overflow failures abort before a collector
starts and remove the incomplete seed. Candidate identity is the exact raw URL;
only the existing baseline-root overlap is removed, while source order is
preserved for the downstream external sort. URL Collection has no Engine
retry/replay or input-sized identity map, and a partial seed never starts a
collector.

Waymore and Katana run concurrently. Complete URL output is read while each
collector is running, admitted against the task target, and passed through a
bounded queue in batches of up to 128 URLs. Uro cleanup and HTTPX verification
start before the collectors finish. HTTPX observations are submitted immediately
through the typed Endpoint v2 stream; the common SDK flushes nonempty batches
at least once per second. Task failure or cancellation never removes batches
already acknowledged by the Server. Unacknowledged data remains subject to the
normal task/session cancellation fences.

The recent-URL cache retains 8,192 SHA-256 identities to reduce repeat probes;
eviction only allows a URL to be rechecked and never discards unique candidates.
Tool output and batch artifacts stay on task-local disk. A 10-second progress
message reports discovered URLs, observed endpoints and processed batches. Empty
or failed HTTPX records do not become fabricated successful observations.

By default, `katana.seed-policy=responsive` requests only stored 2xx/3xx Website
URLs from the current scan or selected target inventory. Other assets remain in
the asset registry. `all` preserves the full Website input. The Engine still
adds the canonical target baselines. `katana.crawl-duration=120` limits each
input crawl independently; `parallelism=10` bounds simultaneous inputs. Limits
restrict coverage, so use the full input policy and larger budgets when needed.

## HTTP Headers

Katana and HTTPX each expose an independent `headers` array with
`type: stringArray`, `format: http-headers`, and default `[]`. Configure each
stage that needs target authentication explicitly; headers are not copied
between stages or forwarded to Waymore's historical providers. Each line is
passed unchanged as a separate `-H` argument. Invalid names, empty values, or
control characters fail before the tool starts without exposing header values
in errors. Duplicate names retain order in argv; wire behavior belongs to the
pinned tool.

Real local-endpoint header regressions use the pinned image-local binaries:

```sh
LUNAFOX_TEST_KATANA_BIN=/absolute/path/to/katana \
LUNAFOX_TEST_HTTPX_BIN=/absolute/path/to/httpx \
go test ./runtime -run TestPinnedHTTPToolsDeliverHeaders -count=1 -v
```

Runtime Image tools and their exact versions are declared in `Dockerfile`,
`requirements.lock`, and the container conformance fixtures. The Python lock
contains direct and transitive Waymore/Uro dependencies, so the runtime image
contains executable CLIs rather than only their entrypoint scripts. Agent task cleanup removes the seed copy,
workspace, tool outputs, and unsubmitted staging on every task terminal.

## Execution Timeout Defaults

Tool/stage defaults in seconds: `waymore.timeout=300`, `katana.timeout=1800`,
`uro.timeout=300`, `httpx.timeout=900`. Katana defaults to depth 2; Katana and
HTTPX use 5-second request timeouts and no request retry. These are development
throughput defaults, not completion guarantees. Explicit saved configurations
retain their values. Updated Engine packages are required for new defaults.
