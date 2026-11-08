package http_test

import (
	"testing"

	. "github.com/enetx/http"
	"github.com/enetx/http/httptest"
)

type testMode string

const (
	http1Mode testMode = "h1"
	http2Mode testMode = "h2"
)

func run(t *testing.T, f func(*testing.T, testMode)) {
	t.Helper()
	for _, mode := range []testMode{http1Mode, http2Mode} {
		t.Run(string(mode), func(t *testing.T) { f(t, mode) })
	}
}

type clientServerTest struct {
	ts *httptest.Server
	c  *Client
}

func newClientServerTest(t testing.TB, mode testMode, h Handler) *clientServerTest {
	t.Helper()
	ts := httptest.NewUnstartedServer(h)
	if mode == http2Mode {
		ts.EnableHTTP2 = true
		ts.StartTLS()
	} else {
		ts.Start()
	}
	t.Cleanup(ts.Close)
	return &clientServerTest{ts: ts, c: ts.Client()}
}
func (cst *clientServerTest) close() { cst.ts.Close() }
