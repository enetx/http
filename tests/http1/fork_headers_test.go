package http_test

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/enetx/http"
	"github.com/enetx/http/httptest"
)

func wireLines(b []byte) []string {
	lines := strings.Split(string(b), "\r\n")
	end := 1
	for end < len(lines) && lines[end] != "" {
		end++
	}
	return lines[1:end]
}

func names(lines []string) []string {
	var result []string
	for _, line := range lines {
		key, _, _ := strings.Cut(line, ":")
		result = append(result, strings.ToLower(key))
	}
	return result
}

func TestForkHTTP1RequestHeaderOrder(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		body         io.Reader
		chunked      bool
		wantLength   string
	}{
		{name: "GET", method: "GET"},
		{name: "POST", method: "POST", body: strings.NewReader("abc"), wantLength: "3"},
		{name: "empty POST", method: "POST", body: http.NoBody, wantLength: "0"},
		{name: "chunked POST", method: "POST", body: io.NopCloser(strings.NewReader("abc")), chunked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, "https://example.com/path", tc.body)
			if err != nil {
				t.Fatal(err)
			}
			req.Header = http.Header{
				"X-Z":                {"z"},
				"X-A":                {"a"},
				"User-Agent":         {"surf-test"},
				http.HeaderOrderKey:  {"x-z", "host", "user-agent", "content-length", "transfer-encoding", "x-a"},
				http.PHeaderOrderKey: {":method", ":authority", ":path", ":scheme"},
			}
			if tc.chunked {
				req.TransferEncoding = []string{"chunked"}
				req.ContentLength = -1
			}
			var b bytes.Buffer
			if err := req.Write(&b); err != nil {
				t.Fatal(err)
			}
			lines := wireLines(b.Bytes())
			got := names(lines)
			want := []string{"x-z", "host", "user-agent"}
			if tc.wantLength != "" {
				want = append(want, "content-length")
			}
			if tc.chunked {
				want = append(want, "transfer-encoding")
			}
			want = append(want, "x-a")
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("header order: got %v, want %v; wire %q", got, want, b.String())
			}
			for _, line := range lines {
				if strings.HasPrefix(line, "Host:") && line != "Host: example.com" {
					t.Fatal(line)
				}
				if strings.HasPrefix(line, "Content-Length:") && line != "Content-Length: "+tc.wantLength {
					t.Fatal(line)
				}
			}
			if tc.chunked && !strings.Contains(b.String(), "\r\n3\r\nabc\r\n0\r\n") {
				t.Fatalf("invalid chunked body: %q", b.String())
			}
		})
	}
}

func TestForkHeaderWriteDoesNotMutateInputs(t *testing.T) {
	h := http.Header{"X-A": {"a"}, "X-Z": {"z"}, http.HeaderOrderKey: {"x-z", "content-length", "x-a"}}
	before := h.Clone()
	exclude := map[string]bool{"X-A": true}
	var b bytes.Buffer
	if err := h.WriteSubset(&b, exclude, 0); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h, before) {
		t.Fatalf("Header changed: %#v", h)
	}
	if !reflect.DeepEqual(exclude, map[string]bool{"X-A": true}) {
		t.Fatalf("exclusion map changed: %#v", exclude)
	}
	if want := "X-Z: z\r\nContent-Length: 0\r\n"; b.String() != want {
		t.Fatalf("got %q, want %q", b.String(), want)
	}
}

func TestForkHeaderOrderDoesNotLeakFromPool(t *testing.T) {
	for i := 0; i < 100; i++ {
		h := http.Header{"X-A": {"a"}, "X-Z": {"z"}}
		want := "X-A: a\r\nX-Z: z\r\n"
		if i%2 == 0 {
			h[http.HeaderOrderKey] = []string{"x-z", "x-a"}
			want = "X-Z: z\r\nX-A: a\r\n"
		}
		var b bytes.Buffer
		if err := h.Write(&b, -1); err != nil {
			t.Fatal(err)
		}
		if b.String() != want {
			t.Fatalf("iteration %d: got %q, want %q", i, b.String(), want)
		}
	}
}

func TestForkResponseHeaderOrderOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if enabled {
				w = http.EnableHeaderOrder(w)
			}
			w.Header()[http.HeaderOrderKey] = []string{"x-z", "x-a"}
			w.Header().Set("X-A", "a")
			w.Header().Set("X-Z", "z")
			w.WriteHeader(http.StatusOK)
		}))
		conn, err := net.DialTimeout("tcp", strings.TrimPrefix(ts.URL, "http://"), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(time.Second))
		io.WriteString(conn, "GET / HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n")
		b, err := io.ReadAll(bufio.NewReader(conn))
		conn.Close()
		ts.Close()
		if err != nil {
			t.Fatal(err)
		}
		lines := names(wireLines(b))
		a, z := -1, -1
		for i, n := range lines {
			if n == "x-a" {
				a = i
			}
			if n == "x-z" {
				z = i
			}
			if n == "header-order" || n == "pheader-order" {
				t.Fatal("metadata leaked")
			}
		}
		if a < 0 || z < 0 || (enabled && z > a) || (!enabled && a > z) {
			t.Fatalf("enabled=%v: order %v", enabled, lines)
		}
	}
}

func TestForkCookieEmbeddedQuotesPreserved(t *testing.T) {
	c := http.Cookie{Name: "token", Value: `abc"def`}
	if err := c.Valid(); err != nil {
		t.Fatal(err)
	}
	if got := c.String(); got != `token=abc"def` {
		t.Fatalf("got %q", got)
	}
	cookies, err := http.ParseCookie(`token=abc"def`)
	if err != nil {
		t.Fatal(err)
	}
	if len(cookies) != 1 || cookies[0].Value != c.Value {
		t.Fatalf("cookies: %#v", cookies)
	}
}
