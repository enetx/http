package http2_test

import (
	"crypto/tls"
	"io"
	"sync/atomic"
	"testing"

	"github.com/enetx/http"
	"github.com/enetx/http/http2"
	"github.com/enetx/http/httptest"
)

func TestPreconnectWarmsHTTP2Pool(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.Write([]byte("ok")) }))
	s.EnableHTTP2 = true
	s.StartTLS()
	defer s.Close()
	tr := &http2.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequest(http.MethodGet, s.URL, nil)
	if err := tr.Preconnect(req); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("preparation sent request")
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.ProtoMajor != 2 || calls.Load() != 1 {
		t.Fatalf("protocol=%s calls=%d", resp.Proto, calls.Load())
	}
	if err := tr.Preconnect(nil); err == nil {
		t.Fatal("accepted nil request")
	}
	if err := tr.Preconnect(&http.Request{}); err == nil {
		t.Fatal("accepted nil URL")
	}
}
