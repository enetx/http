package http_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/enetx/http"
)

func TestHTTP1ExplicitHeaderSpelling(t *testing.T) {
	h := http.Header{
		"Sec-Ch-Ua":         {"brand"},
		"X-Mixed":           {"one", "two"},
		"User-Agent":        {"agent"},
		http.HeaderOrderKey: {"sec-ch-ua", "x-mixed", "user-agent"},
		http.HeaderCaseKey:  {"sec-ch-ua", "x-MiXeD", "User-Agent\r\nInjected: bad"},
	}
	before := h.Clone()
	var got bytes.Buffer
	if err := h.Write(&got, -1); err != nil {
		t.Fatal(err)
	}
	want := "sec-ch-ua: brand\r\nx-MiXeD: one\r\nx-MiXeD: two\r\nUser-Agent: agent\r\n"
	if got.String() != want {
		t.Fatalf("wire %q, want %q", got.String(), want)
	}
	if !reflect.DeepEqual(h, before) {
		t.Fatal("case policy changed caller headers")
	}
	delete(h, http.HeaderCaseKey)
	got.Reset()
	if err := h.Write(&got, -1); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.String(), "Sec-Ch-Ua: brand\r\nX-Mixed: one\r\n") {
		t.Fatalf("canonical defaults changed: %q", got.String())
	}
}
