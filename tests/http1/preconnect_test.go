package http_test

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/enetx/http"
	"github.com/enetx/http/httptest"
)

type preparationBody struct {
	reads, closes atomic.Int32
	io.Reader
}

func (b *preparationBody) Read(p []byte) (int, error) { b.reads.Add(1); return b.Reader.Read(p) }
func (b *preparationBody) Close() error               { b.closes.Add(1); return nil }

func TestPreconnectUsesNormalPoolWithoutSendingRequest(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "http1", true: "http2"}[h2], func(t *testing.T) {
			var requests, connections atomic.Int32
			s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				w.Write(body)
			}))
			s.EnableHTTP2 = h2
			s.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					connections.Add(1)
				}
			}
			s.StartTLS()
			defer s.Close()
			p := new(http.Protocols)
			p.SetHTTP1(true)
			p.SetHTTP2(h2)
			tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, Protocols: p}
			defer tr.CloseIdleConnections()
			body := &preparationBody{Reader: strings.NewReader("one body")}
			req, _ := http.NewRequest(http.MethodPost, s.URL, body)
			if err := tr.Preconnect(req); err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 0 || body.reads.Load() != 0 || body.closes.Load() != 0 {
				t.Fatal("preparation sent a request or touched the body")
			}
			resp, err := tr.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || string(data) != "one body" {
				t.Fatalf("body=%q, err=%v", data, err)
			}
			if h2 && resp.ProtoMajor != 2 {
				t.Fatal(resp.Proto)
			}
			if err := tr.Preconnect(req); err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 1 || connections.Load() != 1 {
				t.Fatalf("requests=%d connections=%d", requests.Load(), connections.Load())
			}
		})
	}
}

func TestPreconnectValidationAndCancellation(t *testing.T) {
	tr := new(http.Transport)
	for _, req := range []*http.Request{nil, {}, {URL: nil}} {
		if tr.Preconnect(req) == nil {
			t.Fatal("accepted missing URL")
		}
	}
	req, _ := http.NewRequest(http.MethodGet, "ftp://example.invalid", nil)
	if tr.Preconnect(req) == nil {
		t.Fatal("accepted unsupported scheme")
	}
	req, _ = http.NewRequest(http.MethodGet, "https://example.invalid", nil)
	tr.DisableKeepAlives = true
	if !errors.Is(tr.Preconnect(req), errors.ErrUnsupported) {
		t.Fatal("accepted disabled keep-alives")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	tr = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { <-ctx.Done(); return nil, context.Cause(ctx) }}
	defer tr.CloseIdleConnections()
	if !errors.Is(tr.Preconnect(req.WithContext(ctx)), context.Canceled) {
		t.Fatal("ignored cancellation")
	}
}
