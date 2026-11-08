# HTTP transports for Surf

A Go **1.27+** fork containing only the HTTP packages used by Surf, the protocol
engines and their tests. One module provides:

- `github.com/enetx/http`: HTTP/1, client/server types and shared configuration.
- `github.com/enetx/http/http2`: the public adapter and frame aliases for the one
  native HTTP/2 engine in `internal/http2`.
- `github.com/enetx/http/http3`: HTTP/3 based on quic-go.
- `cookiejar`, `httptrace`, `httptest`, and the request/response dump helpers in
  `httputil` used by Surf and the protocol tests.

The fork preserves Surf's wire header order, pseudo-header order, ordered
HTTP/2 and HTTP/3 settings, stream IDs, flow control and priority frames.
Profiling, CGI/FastCGI, reverse proxy, old Go compatibility and the old HTTP/2
bundle are outside this fork's scope.

Public-API tests live in `tests/`. White-box protocol tests remain beside their
packages as required by Go.

## Validation

```sh
go generate ./http2
go test -race ./...
go vet ./...
python3 -m unittest discover -s tests/upstream
```

To validate local transport changes from the sibling Surf checkout, temporarily
use a local replacement:

```sh
go mod edit -replace=github.com/enetx/http=../http
go test -race ./...
go mod edit -dropreplace=github.com/enetx/http
```

Published Surf versions depend on a released `http` module without a replacement.

## Updating upstream

[upstream.json](upstream.json) pins each source version and commit.
[UPSTREAM.md](UPSTREAM.md) explains the retained patches and update procedure.
The update helper requires Python 3.9+, Go and Git.

Audit the current fork against its pinned sources:

```sh
python3 scripts/upstream.py audit
```

Prepare a future update, substituting the desired released versions:

```sh
python3 scripts/upstream.py prepare --go go1.27.3 --net v0.62.0 --quic v0.65.0
```

This downloads/caches the sources, merges with the pinned baseline, and writes a
candidate tree, `changes.diff` and `summary.json` in a temporary directory. It
never overwrites this checkout. The candidate includes updated source pins and
module requirements. Resolve any reported conflicts, run the validation commands
and Surf's tests against the candidate, then transfer the reviewed changes.
