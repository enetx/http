package http2_test

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/enetx/http"
	"github.com/enetx/http/internal/http2"
	"golang.org/x/net/http2/hpack"
)

func TestRequestContentLengthFraming(t *testing.T) {
	u, err := url.Parse("https://example.com/upload")
	if err != nil {
		t.Fatal(err)
	}
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
						headers := http.Header{"User-Agent": nil, "X-Before": {"before"}, "X-After": {"after"}}
						if marker.key != "" {
							headers[marker.key] = marker.values
						}
						if order != "none" {
							headers[http.HeaderOrderKey] = []string{"x-before", "x-after"}
							if order == "ordered length" {
								headers[http.HeaderOrderKey] = []string{"x-before", "CONTENT-LENGTH", "x-after"}
							}
						}
						original := headers.Clone()
						reader := strings.NewReader(body.data)
						req := &http2.ClientRequest{Context: context.Background(), Method: method, URL: u, Header: http2.Header(headers), Body: http.NoBody, ContentLength: body.length}
						if body.data != "" {
							req.Body = io.NopCloser(reader)
						}
						if body.replay {
							req.GetBody = func() (io.ReadCloser, error) { panic("encoding must not call GetBody") }
						}
						var block bytes.Buffer
						enc := hpack.NewEncoder(&block)
						var writeErr error
						result, err := http2.EncodeRequestHeaders(req, false, 0, func(name, value string) {
							if err := enc.WriteField(hpack.HeaderField{Name: name, Value: value}); err != nil {
								writeErr = err
							}
						})
						if err != nil || writeErr != nil {
							t.Fatalf("encode: %v; HPACK: %v", err, writeErr)
						}
						fields, err := hpack.NewDecoder(4096, nil).DecodeFull(block.Bytes())
						if err != nil {
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
						for _, field := range fields {
							if field.Name == "content-length" {
								lengths = append(lengths, field.Value)
							}
						}
						if want == "" && len(lengths) != 0 || want != "" && (len(lengths) != 1 || lengths[0] != want) {
							t.Fatalf("Content-Length = %v, want %q; fields %v", lengths, want, fields)
						}
						if result.HasBody != (actualLength != 0) {
							t.Fatalf("HasBody = %v for length %d", result.HasBody, body.length)
						}
						if reader.Len() != len(body.data) || !reflect.DeepEqual(original, headers) {
							t.Fatal("encoding changed the request headers or consumed the body")
						}
					})
				}
			}
		}
	}
}

func TestNilContentLengthUsesSanitizedRequestLength(t *testing.T) {
	u, err := url.Parse("https://example.com/upload")
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []io.ReadCloser{nil, http.NoBody} {
		headers := http.Header{"CONTENT-LENGTH": nil, http.HeaderOrderKey: {"content-length"}}
		original := headers.Clone()
		req := &http2.ClientRequest{Context: context.Background(), Method: http.MethodPatch, URL: u, Header: http2.Header(headers), Body: body, ContentLength: 23}
		_, err := http2.EncodeRequestHeaders(req, false, 0, func(name, value string) {
			if name == "content-length" {
				t.Errorf("sanitized empty body encoded Content-Length: %s", value)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(original, headers) || req.ContentLength != 23 {
			t.Fatal("encoding changed the source request")
		}
	}
}
