package httpcommon_test

import (
	"context"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/enetx/http/httptrace"
	"github.com/enetx/http/internal/headerorder"
	"github.com/enetx/http/internal/httpcommon"
)

type field struct{ name, value string }

func encode(t *testing.T, ctx context.Context, p httpcommon.EncodeHeadersParam) []field {
	t.Helper()
	var fields []field
	_, err := httpcommon.EncodeHeaders(ctx, p, func(name, value string) { fields = append(fields, field{name, value}) })
	if err != nil {
		t.Fatal(err)
	}
	return fields
}
func TestOrderedPseudoHeadersAndContentLength(t *testing.T) {
	u, _ := url.Parse("https://example.com/path?q=1")
	for _, tc := range []struct {
		name, method, protocol string
		want                   []string
	}{
		{name: "GET", method: "GET", want: []string{":method", ":scheme", ":authority", ":path", "x-z", "x-a"}},
		{name: "POST", method: "POST", want: []string{":method", ":scheme", ":authority", ":path", "x-z", "content-length", "x-a"}},
		{name: "CONNECT", method: "CONNECT", want: []string{":method", ":authority", "x-z", "x-a"}},
		{name: "Extended CONNECT", method: "CONNECT", protocol: "websocket", want: []string{":method", ":scheme", ":authority", ":path", ":protocol", "x-z", "x-a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := map[string][]string{"User-Agent": nil, "X-Z": {"z"}, "X-A": {"a"}, headerorder.HeaderOrderKey: {"x-z", "content-length", "x-a"}, headerorder.PHeaderOrderKey: {":method", ":scheme", ":authority", ":path", ":protocol", ":unknown"}, "Content-Length": {"123"}}
			if tc.protocol != "" {
				h[":protocol"] = []string{tc.protocol}
			}
			p := httpcommon.EncodeHeadersParam{Request: httpcommon.Request{URL: u, Method: tc.method, Header: h, ActualContentLength: 0}}
			got := encode(t, context.Background(), p)
			var names []string
			for _, f := range got {
				names = append(names, f.name)
				if f.name == "content-length" && f.value != "0" {
					t.Fatalf("untrusted Content-Length leaked: %q", f.value)
				}
			}
			if !reflect.DeepEqual(names, tc.want) {
				t.Fatalf("got %v want %v", names, tc.want)
			}
			if h["Content-Length"][0] != "123" {
				t.Fatal("header input changed")
			}
		})
	}
}

func TestOrderMetadataDoesNotCountTowardsHeaderLimitOrTrace(t *testing.T) {
	u, _ := url.Parse("https://example.com/")
	h := map[string][]string{"User-Agent": nil, "X-Test": {"value"}, headerorder.HeaderOrderKey: {"x-test", strings.Repeat("metadata", 1024)}, headerorder.PHeaderOrderKey: {":method", ":scheme", ":authority", ":path"}}
	p := httpcommon.EncodeHeadersParam{Request: httpcommon.Request{URL: u, Method: "GET", Header: h}}
	fields := encode(t, context.Background(), p)
	var size uint64
	for _, f := range fields {
		size += uint64(len(f.name) + len(f.value) + 32)
	}
	p.PeerMaxHeaderListSize = size
	var traced []field
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{WroteHeaderField: func(k string, values []string) {
		for _, v := range values {
			traced = append(traced, field{k, v})
		}
	}})
	got := encode(t, ctx, p)
	if !reflect.DeepEqual(got, fields) || !reflect.DeepEqual(traced, fields) {
		t.Fatalf("wire=%v trace=%v want=%v", got, traced, fields)
	}
	p.PeerMaxHeaderListSize--
	if _, err := httpcommon.EncodeHeaders(context.Background(), p, func(string, string) {}); err == nil {
		t.Fatal("real headers should exceed the smaller limit")
	}
}
