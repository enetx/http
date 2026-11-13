package http_test

import (
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/enetx/http"
	"github.com/enetx/http/httptest"
)

// Adapted from Go 1.27.4 TestServerMaxBytesHeaderRace:
// https://github.com/golang/go/commit/4c1752a033aef1a66eadfb11363ff256f8daf47d
func TestServerMaxBytesHeaderRace(t *testing.T) {
	server := httptest.NewServer(http.MaxBytesHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var readers sync.WaitGroup
		defer readers.Wait()
		readers.Go(func() { _, _ = io.ReadAll(r.Body) })
		w.Header().Set("X-Test", "1")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	}), 4))
	defer server.Close()
	client := server.Client()
	for range 20 {
		response, err := client.Post(server.URL, "text/plain", strings.NewReader("hello"))
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode != http.StatusRequestEntityTooLarge || response.Header.Get("X-Test") != "1" {
			t.Fatalf("response: status=%d headers=%v", response.StatusCode, response.Header)
		}
		if !response.Close {
			t.Fatal("HTTP/1 connection remains reusable after the body limit was exceeded")
		}
	}
}
