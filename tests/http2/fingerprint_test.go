package http2_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/enetx/http"
	"github.com/enetx/http/http2"
	"github.com/enetx/http/httptest"
	"golang.org/x/net/http2/hpack"
)

type observedRequest struct {
	settings   []http2.Setting
	priorities []http2.PriorityFrame
	flow       uint32
	streamID   uint32
	priority   http2.PriorityParam
	headers    []hpack.HeaderField
	err        error
}

func rawHTTP2Peer(ln net.Listener, done chan<- observedRequest) {
	var got observedRequest
	defer func() { done <- got }()
	conn, err := ln.Accept()
	if err != nil {
		got.err = err
		return
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	preface := make([]byte, len(http2.ClientPreface))
	if _, got.err = io.ReadFull(conn, preface); got.err != nil {
		return
	}
	if string(preface) != http2.ClientPreface {
		got.err = fmt.Errorf("invalid preface %q", preface)
		return
	}
	fr := http2.NewFramer(conn, conn)
	if got.err = fr.WriteSettings(); got.err != nil {
		return
	}
	var block []byte
	for {
		frame, err := fr.ReadFrame()
		if err != nil {
			got.err = err
			return
		}
		switch f := frame.(type) {
		case *http2.SettingsFrame:
			if !f.IsAck() {
				f.ForeachSetting(func(s http2.Setting) error { got.settings = append(got.settings, s); return nil })
				if got.err = fr.WriteSettingsAck(); got.err != nil {
					return
				}
			}
		case *http2.PriorityFrame:
			got.priorities = append(got.priorities, *f)
		case *http2.WindowUpdateFrame:
			if f.StreamID == 0 {
				got.flow = f.Increment
			}
		case *http2.HeadersFrame:
			got.streamID = f.StreamID
			got.priority = f.Priority
			block = append(block, f.HeaderBlockFragment()...)
			if !f.HeadersEnded() {
				continue
			}
			got.headers, got.err = hpack.NewDecoder(65536, nil).DecodeFull(block)
			if got.err != nil {
				return
			}
			var response bytes.Buffer
			enc := hpack.NewEncoder(&response)
			enc.WriteField(hpack.HeaderField{Name: ":status", Value: "200"})
			enc.WriteField(hpack.HeaderField{Name: "content-length", Value: "2"})
			if got.err = fr.WriteHeaders(http2.HeadersFrameParam{StreamID: f.StreamID, BlockFragment: response.Bytes(), EndHeaders: true}); got.err != nil {
				return
			}
			got.err = fr.WriteData(f.StreamID, true, []byte("ok"))
			return
		default:
			got.err = fmt.Errorf("unexpected pre-request frame %T", frame)
			return
		}
	}
}

func TestFingerprintWireAndHeaderOrder(t *testing.T) {
	settings := []http2.Setting{{ID: http2.SettingHeaderTableSize, Val: 65536}, {ID: http2.SettingEnablePush, Val: 0}, {ID: http2.SettingInitialWindowSize, Val: 6291456}, {ID: http2.SettingMaxFrameSize, Val: 16384}, {ID: http2.SettingNoRFC7540Priorities, Val: 1}}
	priority := http2.PriorityParam{StreamDep: 3, Exclusive: true, Weight: 200}
	prelude := http2.PriorityFrame{FrameHeader: http2.FrameHeader{StreamID: 3}, PriorityParam: http2.PriorityParam{Weight: 50}}
	for _, direct := range []bool{false, true} {
		t.Run(fmt.Sprintf("native-config=%v", direct), func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			done := make(chan observedRequest, 1)
			go rawHTTP2Peer(ln, done)
			var tr http.RoundTripper
			if direct {
				protocols := new(http.Protocols)
				protocols.SetUnencryptedHTTP2(true)
				native := &http.Transport{Protocols: protocols, HTTP2: &http.HTTP2Config{Fingerprint: &http.HTTP2Fingerprint{Settings: settings, StreamID: 5, ConnectionFlow: 15663105, PriorityParam: priority, PriorityFrames: []http2.PriorityFrame{prelude}}}}
				defer native.CloseIdleConnections()
				tr = native
			} else {
				facade := &http2.Transport{AllowHTTP: true, Settings: settings, StreamID: 5, ConnectionFlow: 15663105, PriorityParam: priority, PriorityFrames: []http2.PriorityFrame{prelude}, DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, addr)
				}}
				defer facade.CloseIdleConnections()
				tr = facade
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, "POST", "http://"+ln.Addr().String()+"/path?q=1", http.NoBody)
			if err != nil {
				t.Fatal(err)
			}
			req.Header = http.Header{"X-A": {"a"}, "X-Z": {"z1", "z2"}, "User-Agent": {"surf-test"}, http.HeaderOrderKey: {"x-z", "content-length", "user-agent", "x-a"}, http.PHeaderOrderKey: {":method", ":authority", ":scheme", ":path"}}
			before := req.Header.Clone()
			resp, err := tr.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || string(body) != "ok" {
				t.Fatalf("body=%q err=%v", body, err)
			}
			var got observedRequest
			select {
			case got = <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if got.err != nil {
				t.Fatal(got.err)
			}
			if !reflect.DeepEqual(got.settings, settings) {
				t.Fatalf("SETTINGS: got %v, want %v", got.settings, settings)
			}
			if got.flow != 15663105 || got.streamID != 5 {
				t.Fatalf("flow=%d stream=%d", got.flow, got.streamID)
			}
			if got.priority != priority {
				t.Fatalf("HEADERS priority: got %+v want %+v", got.priority, priority)
			}
			if len(got.priorities) != 1 || got.priorities[0].StreamID != 3 || got.priorities[0].PriorityParam != prelude.PriorityParam {
				t.Fatalf("PRIORITY frames: %+v", got.priorities)
			}
			var names []string
			for _, h := range got.headers {
				names = append(names, h.Name)
			}
			want := []string{":method", ":authority", ":scheme", ":path", "x-z", "x-z", "content-length", "user-agent", "x-a", "accept-encoding"}
			if !reflect.DeepEqual(names, want) {
				t.Fatalf("headers: got %v want %v", names, want)
			}
			if !reflect.DeepEqual(req.Header, before) {
				t.Fatal("request headers changed while encoding")
			}
		})
	}
}

func TestUnknownFrameGapsDoNotPanic(t *testing.T) {
	for _, typ := range []http2.FrameType{0xa, 0xb, 0xc, 0xd, 0xe, 0xf, 0xff} {
		var b bytes.Buffer
		fr := http2.NewFramer(&b, &b)
		if err := fr.WriteRawFrame(typ, 0, 0, []byte("extension")); err != nil {
			t.Fatal(err)
		}
		f, err := fr.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := f.(*http2.UnknownFrame); !ok {
			t.Fatalf("type=%x decoded as %T", typ, f)
		}
	}
}

func TestFingerprintRejectsInvalidConfiguration(t *testing.T) {
	for _, fp := range []*http.HTTP2Fingerprint{
		{StreamID: 2},
		{StreamID: 0x80000001},
		{ConnectionFlow: 0x80000000},
		{Settings: []http2.Setting{{ID: http2.SettingEnablePush, Val: 2}}},
		{Settings: []http2.Setting{{ID: http2.SettingMaxFrameSize, Val: 1}}},
		{Settings: []http2.Setting{{ID: 1, Val: 1}, {ID: 1, Val: 2}}},
	} {
		t.Run(fmt.Sprintf("%+v", fp), func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			accepted := make(chan struct{})
			go func() {
				defer close(accepted)
				c, err := ln.Accept()
				if err == nil {
					defer c.Close()
					c.SetReadDeadline(time.Now().Add(time.Second))
					io.Copy(io.Discard, c)
				}
			}()
			p := new(http.Protocols)
			p.SetUnencryptedHTTP2(true)
			tr := http.Transport{Protocols: p, HTTP2: &http.HTTP2Config{Fingerprint: fp}}
			defer tr.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+ln.Addr().String(), nil)
			_, err = tr.RoundTrip(req)
			if err == nil || !strings.Contains(err.Error(), "http2:") {
				t.Fatalf("expected HTTP/2 config error, got %v", err)
			}
			ln.Close()
			<-accepted
		})
	}
}

func TestFingerprintResponseHeaderLimit(t *testing.T) {
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Large", strings.Repeat("x", 1024))
		io.WriteString(w, "ok")
	}))
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()
	for _, limited := range []bool{false, true} {
		tr := &http2.Transport{TLSClientConfig: ts.Client().Transport.(*http.Transport).TLSClientConfig.Clone()}
		if limited {
			tr.Settings = []http2.Setting{{ID: http2.SettingMaxHeaderListSize, Val: 128}}
		}
		req, _ := http.NewRequest("GET", ts.URL, nil)
		resp, err := tr.RoundTrip(req)
		if limited {
			if err == nil {
				resp.Body.Close()
				t.Fatal("expected header limit error")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		tr.CloseIdleConnections()
	}
}
