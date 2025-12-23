package http_test

import (
	"bytes"
	"testing"

	"github.com/enetx/http"
)

var headerWriteTests = []struct {
	h        http.Header
	exclude  map[string]bool
	expected string
}{
	{http.Header{}, nil, ""},
	{
		http.Header{
			"Content-Type":   {"text/html; charset=UTF-8"},
			"Content-Length": {"0"},
		},
		nil,
		"Content-Length: 0\r\nContent-Type: text/html; charset=UTF-8\r\n",
	},
	{
		http.Header{
			"Content-Length": {"0", "1", "2"},
		},
		nil,
		"Content-Length: 0\r\nContent-Length: 1\r\nContent-Length: 2\r\n",
	},
	{
		http.Header{
			"Expires":          {"-1"},
			"Content-Length":   {"0"},
			"Content-Encoding": {"gzip"},
		},
		map[string]bool{"Content-Length": true},
		"Content-Encoding: gzip\r\nExpires: -1\r\n",
	},
	{
		http.Header{
			"Expires":          {"-1"},
			"Content-Length":   {"0", "1", "2"},
			"Content-Encoding": {"gzip"},
		},
		map[string]bool{"Content-Length": true},
		"Content-Encoding: gzip\r\nExpires: -1\r\n",
	},
	{
		http.Header{
			"Expires":          {"-1"},
			"Content-Length":   {"0"},
			"Content-Encoding": {"gzip"},
		},
		map[string]bool{"Content-Length": true, "Expires": true, "Content-Encoding": true},
		"",
	},
	{
		http.Header{
			"Nil":          nil,
			"Empty":        {},
			"Blank":        {""},
			"Double-Blank": {"", ""},
		},
		nil,
		"Blank: \r\nDouble-Blank: \r\nDouble-Blank: \r\n",
	},
	// Tests header sorting when over the insertion sort threshold side:
	{
		http.Header{
			"k1": {"1a", "1b"},
			"k2": {"2a", "2b"},
			"k3": {"3a", "3b"},
			"k4": {"4a", "4b"},
			"k5": {"5a", "5b"},
			"k6": {"6a", "6b"},
			"k7": {"7a", "7b"},
			"k8": {"8a", "8b"},
			"k9": {"9a", "9b"},
		},
		map[string]bool{"k5": true},
		"k1: 1a\r\nk1: 1b\r\nk2: 2a\r\nk2: 2b\r\nk3: 3a\r\nk3: 3b\r\n" +
			"k4: 4a\r\nk4: 4b\r\nk6: 6a\r\nk6: 6b\r\n" +
			"k7: 7a\r\nk7: 7b\r\nk8: 8a\r\nk8: 8b\r\nk9: 9a\r\nk9: 9b\r\n",
	},
	// Test sorting headers by the special Header-Order header
	{
		http.Header{
			"a":                 {"2"},
			"b":                 {"3"},
			"e":                 {"1"},
			"c":                 {"5"},
			"d":                 {"4"},
			http.HeaderOrderKey: {"e", "a", "b", "d", "c"},
		},
		nil,
		"e: 1\r\na: 2\r\nb: 3\r\nd: 4\r\nc: 5\r\n",
	},
	{
		http.Header{
			"MESH-Commerce-Channel": {"android-app-phone"},
			"X-acf-sensor-data":     {"3456"},
			"User-Agent":            {"size/3.1.0.8355 (android-app-phone; Android 10; Build/CPH2185_11_A.28)"},
			"X-Request-Auth":        {"hawkHeader"},
			"X-NewRelic-ID":         {"12345"},
			"Accept-Encoding":       {"gzip"},
			"Connection":            {"Keep-Alive"},
			"Content-Type":          {"application/json; charset=UTF-8"},
			"Accept":                {"application/json"},
			"x-api-key":             {"ABCDEFGHIJKLMNOPQRSTUVWXYZ"},
			"Transfer-Encoding":     {"chunked"},
			"mesh-version":          {"cart=4"},
			"Host":                  {"prod.jdgroupmesh.cloud"},
			http.HeaderOrderKey: {
				"x-newrelic-id",
				"x-api-key",
				"mesh-commerce-channel",
				"mesh-version",
				"user-agent",
				"x-request-auth",
				"x-acf-sensor-data",
				"content-type",
				"accept",
				"transfer-encoding",
				"host",
				"connection",
				"accept-encoding",
			},
		},
		nil,
		"X-NewRelic-ID: 12345\r\nx-api-key: ABCDEFGHIJKLMNOPQRSTUVWXYZ\r\nMESH-Commerce-Channel: android-app-phone\r\n" +
			"mesh-version: cart=4\r\nUser-Agent: size/3.1.0.8355 (android-app-phone; Android 10; Build/CPH2185_11_A.28)\r\n" +
			"X-Request-Auth: hawkHeader\r\nX-acf-sensor-data: 3456\r\nContent-Type: application/json; charset=UTF-8\r\n" +
			"Accept: application/json\r\nTransfer-Encoding: chunked\r\nHost: prod.jdgroupmesh.cloud\r\nConnection: Keep-Alive\r\n" +
			"Accept-Encoding: gzip\r\n",
	},
}

func TestHeaderWrite(t *testing.T) {
	var buf bytes.Buffer
	for i, test := range headerWriteTests {
		test.h.WriteSubset(&buf, test.exclude, 0)
		if buf.String() != test.expected {
			t.Errorf("#%d:\n got: %q\nwant: %q", i, buf.String(), test.expected)
		}
		buf.Reset()
	}
}
