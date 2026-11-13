package http_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enetx/http"
	"github.com/enetx/http/http2"
	"github.com/enetx/http/httptest"
	"github.com/enetx/http/httptrace"
)

// External TLS providers expose the standard state signature without being a
// concrete *tls.Conn. The linked transport must honor their selected protocol.
type providerTLSConn struct{ *tls.Conn }

func TestExternalTLSHTTP1PoolDoesNotWaitForLeaderResponse(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var dialing, received, handshakes atomic.Int32
	bothDialing, bothRequests := make(chan struct{}), make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if received.Add(1) == 2 {
			close(bothRequests)
		}
		select {
		case <-bothRequests:
		case <-ctx.Done():
			return
		}
		if r.ProtoMajor != 1 {
			t.Errorf("protocol=%d", r.ProtoMajor)
		}
		io.WriteString(w, r.URL.Path)
	}))
	server.TLS = &tls.Config{NextProtos: []string{"http/1.1"}, GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		handshakes.Add(1)
		select {
		case <-bothDialing:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport := &http.Transport{}
	linked, err := http2.ConfigureTransports(transport)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	linked.DialTLSContext = func(ctx context.Context, network, address string, _ *tls.Config) (net.Conn, error) {
		raw, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		host, _, _ := net.SplitHostPort(address)
		secure := tls.Client(raw, &tls.Config{RootCAs: roots, ServerName: host, NextProtos: []string{"h2", "http/1.1"}})
		if err = secure.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, err
		}
		return &providerTLSConn{secure}, nil
	}
	trace := &httptrace.ClientTrace{GetConn: func(string) {
		if dialing.Add(1) == 2 {
			close(bothDialing)
		}
	}}
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for i := range 2 {
		wg.Go(func() {
			request, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, fmt.Sprintf("%s/%d", server.URL, i), nil)
			if err != nil {
				errors <- err
				return
			}
			response, err := linked.RoundTrip(request)
			if err != nil {
				errors <- err
				return
			}
			defer response.Body.Close()
			data, err := io.ReadAll(response.Body)
			if err == nil && string(data) != fmt.Sprintf("/%d", i) {
				err = fmt.Errorf("response=%q", data)
			}
			errors <- err
		})
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := handshakes.Load(); n != 2 {
		t.Errorf("handshakes=%d", n)
	}
}
