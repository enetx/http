package http_test

import (
	"bufio"
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/enetx/http"
)

func TestRequestContentLengthFraming(t *testing.T) {
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		for _, body := range []struct {
			name, data string
			length     int64
			replay     bool
		}{
			{name: "absent"},
			{name: "explicit empty upload", replay: true},
			{name: "known upload", data: "abc", length: 3},
			{name: "unknown upload", data: "abc", length: -1, replay: true},
			{name: "unknown zero field", data: "abc", replay: true},
		} {
			for _, marker := range []struct {
				name, key string
				values    []string
				omit      bool
			}{
				{name: "upstream default"},
				{name: "nil canonical", key: "Content-Length", omit: true},
				{name: "nil mixed case", key: "cOnTeNt-LeNgTh", omit: true},
				{name: "empty slice", key: "content-length", values: []string{}, omit: true},
				{name: "untrusted value", key: "cOnTeNt-LeNgTh", values: []string{"999", "not a length"}},
				{name: "empty string is not nil", key: "Content-Length", values: []string{""}},
			} {
				for _, order := range []string{"none", "unlisted length", "ordered length"} {
					t.Run(method+"/"+body.name+"/"+marker.name+"/"+order, func(t *testing.T) {
						req, err := http.NewRequest(method, "https://example.com/upload", nil)
						if err != nil {
							t.Fatal(err)
						}
						if body.data != "" {
							req.Body = io.NopCloser(strings.NewReader(body.data))
						}
						req.ContentLength = body.length
						if body.replay {
							req.GetBody = func() (io.ReadCloser, error) { panic("serialization must not call GetBody") }
						}
						req.Header = http.Header{"User-Agent": nil, "X-Before": {"before"}, "X-After": {"after"}}
						if marker.key != "" {
							req.Header[marker.key] = marker.values
						}
						if order != "none" {
							req.Header[http.HeaderOrderKey] = []string{"x-before", "HOST", "TRANSFER-ENCODING", "x-after"}
							if order == "ordered length" {
								req.Header[http.HeaderOrderKey] = []string{"x-before", "HOST", "CONTENT-LENGTH", "TRANSFER-ENCODING", "x-after"}
							}
						}
						original := req.Header.Clone()
						var wire bytes.Buffer
						if err := req.Write(&wire); err != nil {
							t.Fatal(err)
						}
						actualLength := body.length
						if body.data != "" && actualLength == 0 {
							actualLength = -1
						}
						want := ""
						if actualLength > 0 {
							want = "3"
						} else if actualLength == 0 && !marker.omit && (method == "POST" || method == "PUT" || method == "PATCH" || body.replay && order == "ordered length") {
							want = "0"
						}
						var lengths []string
						for _, line := range wireLines(wire.Bytes()) {
							name, value, _ := strings.Cut(line, ":")
							if strings.EqualFold(name, "content-length") {
								lengths = append(lengths, strings.TrimSpace(value))
							}
						}
						if want == "" && len(lengths) != 0 || want != "" && (len(lengths) != 1 || lengths[0] != want) {
							t.Fatalf("Content-Length = %v, want %q; wire %q", lengths, want, wire.String())
						}
						parsed, err := http.ReadRequest(bufio.NewReader(&wire))
						if err != nil {
							t.Fatal(err)
						}
						data, err := io.ReadAll(parsed.Body)
						parsed.Body.Close()
						if err != nil || string(data) != body.data {
							t.Fatalf("decoded body %q, err %v; want %q", data, err, body.data)
						}
						if !reflect.DeepEqual(original, req.Header) {
							t.Fatalf("request headers changed: got %#v, want %#v", req.Header, original)
						}
					})
				}
			}
		}
	}
}

func TestResponseNilContentLengthKeepsDefaults(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		for _, marker := range []bool{false, true} {
			r := &http.Response{StatusCode: 200, ProtoMajor: 1, ProtoMinor: 1, Header: make(http.Header), Body: http.NoBody, Request: &http.Request{Method: method}}
			if marker {
				r.Header["Content-Length"] = nil
			}
			var wire bytes.Buffer
			if err := r.Write(&wire); err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(wire.String(), "Content-Length: 0\r\n"); n != 1 {
				t.Fatalf("method %s, marker %v: Content-Length count %d; wire %q", method, marker, n, wire.String())
			}
		}
	}
}

func TestHeaderWriteNilContentLengthKeepsExplicitLength(t *testing.T) {
	for _, length := range []int64{-1, 0, 3} {
		h := http.Header{"Content-Length": nil, http.HeaderOrderKey: {"content-length"}}
		original := h.Clone()
		var wire bytes.Buffer
		if err := h.Write(&wire, length); err != nil {
			t.Fatal(err)
		}
		want := ""
		if length == 0 {
			want = "Content-Length: 0\r\n"
		} else if length == 3 {
			want = "Content-Length: 3\r\n"
		}
		if wire.String() != want || !reflect.DeepEqual(h, original) {
			t.Fatalf("length %d: wire %q, want %q; input changed: %v", length, wire.String(), want, !reflect.DeepEqual(h, original))
		}
	}
}

func TestNilContentLengthUsesSanitizedRequestLength(t *testing.T) {
	req, err := http.NewRequest(http.MethodPatch, "https://example.com/upload", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = 23
	req.Header["CONTENT-LENGTH"] = nil
	req.Header[http.HeaderOrderKey] = []string{"content-length"}
	original := req.Header.Clone()
	var wire bytes.Buffer
	if err := req.Write(&wire); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(wire.String(), "Content-Length:") || !reflect.DeepEqual(req.Header, original) || req.ContentLength != 23 {
		t.Fatalf("sanitized empty request: wire %q, headers %#v, length %d", wire.String(), req.Header, req.ContentLength)
	}
}
