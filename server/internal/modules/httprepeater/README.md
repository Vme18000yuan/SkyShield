# HTTP Repeater

An authenticated, explicit single-request tool. `POST /v1/httpRepeater:send`
accepts one HTTP/1.0, HTTP/1.1 or HTTP/2 text representation and an independent connection URL.
The UI requires only Raw text: its transport service derives that connection
URL from the absolute request target or Host header and passes it to this
existing API. No manual destination field or fallback resend is used.
The derived URL preserves the supplied authority: omitted ports stay omitted,
and explicit ports (including 80/443) are retained. Default ports are resolved
only when opening a socket or proxy tunnel; Raw Host and HTTP/2 authority are
not reconstructed from the protocol's default port.
Admin/analyst membership and global/applicable target blacklists are checked
before dialing. The actual destination IP is checked and pinned for both direct
connections and CONNECT tunnels through the system proxy. No redirects,
automatic HTTP retries, cookie jar, or authentication from the platform session
are forwarded. TLS verification defaults on.

Headers are serialized with CRLF; body bytes are the submitted UTF-8 text.
Content-Length repair is explicit (on by default); invalid/multiple-message
framing is rejected. HTTP/1.x raw response bytes, including chunk framing and repeated
headers, are captured separately from parsing. Binary data remains available
through the base64/download representation. Bounded requests/responses,
deadlines, disconnect cancellation and concurrency caps apply. A short-lived
in-memory receipt prevents accidental reuse of an attempt ID; request/response
payloads are not persisted by the server. Each manual resend uses a new ID.

The frontend owns ephemeral tabs/history, with no network activity on page
load, tab selection or history navigation. Requests run through hooks/services;
the shared HTTP Raw viewer owns response presentation.

HTTP/2 uses a single `http2.ClientConn.RoundTrip` on the authorized, pinned
connection. HTTPS requires ALPN `h2`; explicit HTTP uses prior-knowledge h2c.
No automatic downgrade, redirect or replay is performed. Hop-by-hop headers
are removed and disclosed; Transfer-Encoding is rejected rather than changing
chunk framing silently. Requests/results expose their protocol. HTTP/2 response
Raw/Hex/download is a decoded message representation, not a binary frame capture.
References: https://pkg.go.dev/golang.org/x/net/http2 and RFC 9113.
