package h2c_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/enetx/http"
	"github.com/enetx/http/http2"
	"github.com/enetx/http/http2/h2c"
	"github.com/enetx/http/httptest"
	"golang.org/x/net/http2/hpack"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, "%s %s %s", r.Method, r.URL.Path, body)
	})
	s := httptest.NewServer(h2c.NewHandler(h, &http2.Server{}))
	t.Cleanup(s.Close)
	return s
}

func TestHTTP1AndPriorKnowledge(t *testing.T) {
	s := newServer(t)
	for _, proto := range []int{1, 2} {
		t.Run(fmt.Sprintf("http%d", proto), func(t *testing.T) {
			client := s.Client()
			if proto == 2 {
				tr := &http2.Transport{
					AllowHTTP: true,
					DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
						return new(net.Dialer).DialContext(ctx, network, addr)
					},
				}
				t.Cleanup(tr.CloseIdleConnections)
				client = &http.Client{Transport: tr}
			}
			client.Timeout = 5 * time.Second
			resp, err := client.Post(s.URL+"/body", "text/plain", strings.NewReader("payload"))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.ProtoMajor != proto || resp.StatusCode != http.StatusOK || string(body) != "POST /body payload" {
				t.Fatalf("response = %s %d %q", resp.Proto, resp.StatusCode, body)
			}
		})
	}
}

func TestUpgradeAndSubsequentRequest(t *testing.T) {
	s := newServer(t)
	conn, err := net.DialTimeout("tcp", s.Listener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var settings bytes.Buffer
	if err := http2.NewFramer(&settings, nil).WriteSettings(http2.Setting{ID: http2.SettingEnablePush, Val: 0}); err != nil {
		t.Fatal(err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(settings.Bytes()[9:])
	_, err = fmt.Fprintf(conn, "POST /upgrade HTTP/1.1\r\nHost: example.test\r\nConnection: Upgrade, HTTP2-Settings\r\nUpgrade: h2c\r\nHTTP2-Settings: %s\r\nContent-Length: 7\r\n\r\npayload", encoded)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Upgrade") != "h2c" {
		t.Fatalf("upgrade response = %s, headers = %v", resp.Status, resp.Header)
	}
	if _, err := io.WriteString(conn, http2.ClientPreface); err != nil {
		t.Fatal(err)
	}
	fr := http2.NewFramer(conn, reader)
	fr.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	if err := fr.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	readResponse(t, fr, 1, "POST /upgrade payload")

	var block bytes.Buffer
	enc := hpack.NewEncoder(&block)
	for _, field := range []hpack.HeaderField{
		{Name: ":method", Value: "GET"},
		{Name: ":scheme", Value: "http"},
		{Name: ":authority", Value: "example.test"},
		{Name: ":path", Value: "/next"},
	} {
		if err := enc.WriteField(field); err != nil {
			t.Fatal(err)
		}
	}
	if err := fr.WriteHeaders(http2.HeadersFrameParam{StreamID: 3, BlockFragment: block.Bytes(), EndHeaders: true, EndStream: true}); err != nil {
		t.Fatal(err)
	}
	readResponse(t, fr, 3, "GET /next ")
}

func readResponse(t *testing.T, fr *http2.Framer, streamID uint32, want string) {
	t.Helper()
	var body strings.Builder
	status := ""
	for {
		frame, err := fr.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		end := false
		switch f := frame.(type) {
		case *http2.SettingsFrame:
			if !f.IsAck() {
				if err := fr.WriteSettingsAck(); err != nil {
					t.Fatal(err)
				}
			}
		case *http2.MetaHeadersFrame:
			if f.StreamID != streamID {
				t.Fatalf("headers on stream %d, want %d", f.StreamID, streamID)
			}
			if s := f.PseudoValue("status"); s != "" {
				status = s
			}
			end = f.StreamEnded()
		case *http2.DataFrame:
			if f.StreamID != streamID {
				t.Fatalf("data on stream %d, want %d", f.StreamID, streamID)
			}
			body.Write(f.Data())
			end = f.StreamEnded()
		case *http2.RSTStreamFrame, *http2.GoAwayFrame:
			t.Fatalf("connection rejected request: %v", frame)
		}
		if end {
			if status != "200" || body.String() != want {
				t.Fatalf("stream %d: status %q, body %q; want 200 %q", streamID, status, body.String(), want)
			}
			return
		}
	}
}
