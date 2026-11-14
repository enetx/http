//go:build !nethttpomithttp2

package http_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enetx/http"
	"github.com/enetx/http/http2"
	"github.com/enetx/http/httptest"
	"github.com/enetx/http/httptrace"
)

type poolProbeBody struct{ reads, closes atomic.Int32 }

func (b *poolProbeBody) Read([]byte) (int, error) { b.reads.Add(1); return 0, io.EOF }
func (b *poolProbeBody) Close() error             { b.closes.Add(1); return nil }

func TestHTTP2PoolSnapshotDoesNotDialReserveOrSend(t *testing.T) {
	for _, adapter := range []bool{false, true} {
		name := "native"
		if adapter {
			name = "adapter"
		}
		t.Run(name, func(t *testing.T) {
			var requests, dials atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				io.WriteString(w, "ok")
			}))
			server.EnableHTTP2 = true
			server.Config.HTTP2 = &http.HTTP2Config{MaxConcurrentStreams: 1}
			server.StartTLS()
			defer server.Close()

			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			transport := &http.Transport{
				ForceAttemptHTTP2: true,
				TLSClientConfig:   &tls.Config{RootCAs: roots},
				DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					dials.Add(1)
					return (&net.Dialer{}).DialContext(ctx, network, address)
				},
			}
			defer transport.CloseIdleConnections()

			has := transport.HasHTTP2Connection
			prepare := transport.Preconnect
			roundTrip := transport.RoundTrip
			if adapter {
				linked, err := http2.ConfigureTransports(transport)
				if err != nil {
					t.Fatal(err)
				}
				linked.TLSClientConfig = transport.TLSClientConfig
				linked.DialTLSContext = func(ctx context.Context, network, address string, config *tls.Config) (net.Conn, error) {
					dials.Add(1)
					return (&tls.Dialer{Config: config}).DialContext(ctx, network, address)
				}
				has, prepare, roundTrip = linked.HasHTTP2Connection, linked.Preconnect, linked.RoundTrip
			}

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			body := new(poolProbeBody)
			probe := req.Clone(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
				GetConn: func(string) { t.Error("pool snapshot invoked GetConn") },
				GotConn: func(httptrace.GotConnInfo) { t.Error("pool snapshot invoked GotConn") },
			}))
			probe.Body = body
			if has(probe) || dials.Load() != 0 || requests.Load() != 0 {
				t.Fatal("cold snapshot opened or reported a connection")
			}
			if err := prepare(req); err != nil {
				t.Fatal(err)
			}

			var workers sync.WaitGroup
			for range 8 {
				workers.Go(func() {
					for range 64 {
						if !has(probe) {
							t.Error("warm H2 session was not observable")
							return
						}
					}
				})
			}
			workers.Wait()
			if body.reads.Load() != 0 || body.closes.Load() != 0 || requests.Load() != 0 || dials.Load() != 1 {
				t.Fatalf("pool snapshots: reads=%d closes=%d requests=%d dials=%d",
					body.reads.Load(), body.closes.Load(), requests.Load(), dials.Load())
			}

			other := req.Clone(req.Context())
			other.URL.Host = "other.invalid:443"
			if has(other) {
				t.Fatal("different authority inherited the session")
			}
			transport.Proxy = http.ProxyURL(&url.URL{Scheme: "http", Host: "proxy.invalid:8080"})
			if has(req) {
				t.Fatal("another proxy route inherited the direct session")
			}
			transport.Proxy = func(*http.Request) (*url.URL, error) { return nil, errors.New("proxy selection failed") }
			if has(req) {
				t.Fatal("proxy error was treated as availability")
			}
			transport.Proxy = nil

			ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
			defer cancel()
			response, err := roundTrip(req.WithContext(ctx))
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.ProtoMajor != 2 || dials.Load() != 1 || requests.Load() != 1 {
				t.Fatal("snapshot reserved a stream or changed subsequent pool reuse")
			}
			transport.CloseIdleConnections()
			if has(req) {
				t.Fatal("idle cleanup left a reusable session")
			}
		})
	}
}

func TestHTTP2PoolSnapshotBusySessionAndGOAWAY(t *testing.T) {
	finished := make(chan struct{})
	defer close(finished)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		select {
		case <-finished:
		case <-r.Context().Done():
		}
	}))
	server.EnableHTTP2 = true
	server.Config.HTTP2 = &http.HTTP2Config{MaxConcurrentStreams: 1}
	server.StartTLS()
	defer server.CloseClientConnections()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	response, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if !transport.HasHTTP2Connection(req) {
		t.Fatal("the stream limit must not hide an otherwise reusable H2 session")
	}

	shutdown := make(chan error, 1)
	go func() { shutdown <- server.Config.Shutdown(ctx) }()
	for transport.HasHTTP2Connection(req) {
		select {
		case <-ctx.Done():
			t.Fatal("GOAWAY did not retire the pooled session")
		case <-time.After(time.Millisecond):
		}
	}
	response.Body.Close()
	cancel()
	<-shutdown
}

func TestHTTP2PoolSnapshotRejectsInvalidAndHTTP1Requests(t *testing.T) {
	transport := new(http.Transport)
	req, _ := http.NewRequest(http.MethodGet, "https://example.invalid", nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	closed := req.Clone(t.Context())
	closed.Close = true
	for _, input := range []*http.Request{nil, {}, req.WithContext(ctx), closed} {
		if transport.HasHTTP2Connection(input) {
			t.Fatal("invalid or closed request has an H2 session")
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") }))
	defer server.Close()
	defer transport.CloseIdleConnections()
	req, _ = http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	if err := transport.Preconnect(req); err != nil {
		t.Fatal(err)
	}
	if transport.HasHTTP2Connection(req) {
		t.Fatal("an HTTP/1 connection was mistaken for H2")
	}
}
