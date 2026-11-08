package httptrace_test

import (
	"io"
	"sync/atomic"
	"testing"

	"github.com/enetx/http"
	"github.com/enetx/http/httptest"
	"github.com/enetx/http/httptrace"
)

func TestNetDialerReceivesForkTrace(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer ts.Close()
	var started, finished atomic.Int32
	req, err := http.NewRequest("GET", ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{ConnectStart: func(string, string) { started.Add(1) }, ConnectDone: func(string, string, error) { finished.Add(1) }}))
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if started.Load() != 1 || finished.Load() != 1 {
		t.Fatalf("connect hooks: start=%d done=%d", started.Load(), finished.Load())
	}
}
