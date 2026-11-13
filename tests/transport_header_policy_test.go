package http_test

import (
	"context"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/enetx/http"
	"github.com/enetx/http/httptest"
)

type headerPreparationBody struct {
	io.Reader
	reads, closes atomic.Int32
}

func (b *headerPreparationBody) Read(p []byte) (int, error) { b.reads.Add(1); return b.Reader.Read(p) }
func (b *headerPreparationBody) Close() error               { b.closes.Add(1); return nil }

func TestPrepareHeadersUsesSelectedProtocolAndPrivateRequest(t *testing.T) {
	for _, protocol := range []int{1, 2} {
		t.Run(strconv.Itoa(protocol), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("X-Protocol"); got != strconv.Itoa(r.ProtoMajor) {
					t.Errorf("prepared=%s wire=%d", got, r.ProtoMajor)
				}
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				w.Write(data)
			}))
			server.EnableHTTP2 = protocol == 2
			server.StartTLS()
			defer server.Close()
			transport := server.Client().Transport.(*http.Transport).Clone()
			defer transport.CloseIdleConnections()
			var active *headerPreparationBody
			contextKey := new(int)
			transport.PrepareHeaders = func(req *http.Request, actual int) {
				*req = *req.WithContext(context.WithValue(req.Context(), contextKey, "prepared"))
				calls.Add(1)
				if actual != protocol {
					t.Errorf("protocol=%d want %d", actual, protocol)
				}
				if active.reads.Load() != 0 {
					t.Error("preparation read the request body")
				}
				req.Header.Set("X-Protocol", strconv.Itoa(actual))
				req.Header["X-Caller"][0] = "private"
			}
			client := &http.Client{Transport: transport}
			for range 2 {
				body := &headerPreparationBody{Reader: strings.NewReader("streamed payload")}
				active = body
				request, err := http.NewRequest(http.MethodPost, server.URL, body)
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("X-Caller", "original")
				response, err := client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if response.Request.Context().Value(contextKey) != "prepared" || request.Context().Value(contextKey) != nil {
					t.Fatal("prepared context metadata was lost or leaked into caller")
				}
				if string(data) != "streamed payload" {
					t.Errorf("body=%q", data)
				}
				if request.Header.Get("X-Caller") != "original" || request.Header.Get("X-Protocol") != "" {
					t.Fatal("caller headers changed")
				}
				if body.closes.Load() != 1 {
					t.Errorf("body closes=%d", body.closes.Load())
				}
			}
			if calls.Load() != 2 {
				t.Errorf("preparations=%d", calls.Load())
			}
		})
	}
}

func TestPrepareHeadersValidationAndClone(t *testing.T) {
	for _, protocol := range []int{1, 2} {
		t.Run(strconv.Itoa(protocol), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); io.WriteString(w, "ok") }))
			server.EnableHTTP2 = protocol == 2
			server.StartTLS()
			defer server.Close()
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.PrepareHeaders = func(req *http.Request, _ int) { req.Header.Set("X-Invalid", "bad\r\nInjected: value") }
			clone := transport.Clone()
			defer clone.CloseIdleConnections()
			defer transport.CloseIdleConnections()
			request, err := http.NewRequest(http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := clone.RoundTrip(request)
			if response != nil {
				response.Body.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "invalid header") {
				t.Fatalf("expected invalid header error, got %v", err)
			}
			if requests.Load() != 0 {
				t.Fatalf("invalid request reached peer: %d", requests.Load())
			}
			if request.Header.Get("X-Invalid") != "" {
				t.Fatal("caller headers changed")
			}
		})
	}
}

func TestPrepareHeadersDoesNotRunOnPreconnect(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") }))
	defer server.Close()
	transport := server.Client().Transport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	transport.PrepareHeaders = func(*http.Request, int) { calls.Add(1) }
	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = transport.Preconnect(request); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("preparation ran without a request")
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if calls.Load() != 1 {
		t.Errorf("preparations=%d", calls.Load())
	}
}
