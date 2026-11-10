package http2_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enetx/http"
	"github.com/enetx/http/http2"
	"golang.org/x/net/http2/hpack"
)

func TestFingerprintFirstStreamGOAWAY(t *testing.T) {
	t.Parallel()
	for _, direct := range []bool{false, true} {
		for _, start := range []uint32{1, 3, 17} {
			for _, prelude := range []bool{false, true} {
				for _, code := range []http2.ErrCode{http2.ErrCodeHTTP11Required, http2.ErrCodeInternal, http2.ErrCodeNo} {
					t.Run(fmt.Sprintf("native=%v/start=%d/prelude=%v/code=%v", direct, start, prelude, code), func(t *testing.T) {
						t.Parallel()
						listener, err := net.Listen("tcp", "127.0.0.1:0")
						if err != nil {
							t.Fatal(err)
						}
						defer listener.Close()
						var requests atomic.Int32
						go func() {
							for {
								conn, err := listener.Accept()
								if err != nil {
									return
								}
								go func() {
									defer conn.Close()
									conn.SetDeadline(time.Now().Add(3 * time.Second))
									preface := make([]byte, len(http2.ClientPreface))
									if _, err := io.ReadFull(conn, preface); err != nil {
										return
									}
									fr := http2.NewFramer(conn, conn)
									if err := fr.WriteSettings(); err != nil {
										return
									}
									for {
										frame, err := fr.ReadFrame()
										if err != nil {
											return
										}
										switch f := frame.(type) {
										case *http2.SettingsFrame:
											if !f.IsAck() {
												fr.WriteSettingsAck()
											}
										case *http2.HeadersFrame:
											if requests.Add(1) == 1 {
												fr.WriteGoAway(0, code, nil)
											} else {
												var block bytes.Buffer
												hpack.NewEncoder(&block).WriteField(hpack.HeaderField{Name: ":status", Value: "200"})
												fr.WriteHeaders(http2.HeadersFrameParam{StreamID: f.StreamID, EndHeaders: true, EndStream: true, BlockFragment: block.Bytes()})
											}
											return
										}
									}
								}()
							}
						}()
						fingerprint := &http.HTTP2Fingerprint{StreamID: start}
						if prelude {
							fingerprint.PriorityFrames = []http2.PriorityFrame{{FrameHeader: http2.FrameHeader{StreamID: start + 2}}}
						}
						var transport http.RoundTripper
						if direct {
							protocols := new(http.Protocols)
							protocols.SetUnencryptedHTTP2(true)
							tr := &http.Transport{Protocols: protocols, HTTP2: &http.HTTP2Config{Fingerprint: fingerprint}}
							defer tr.CloseIdleConnections()
							transport = tr
						} else {
							tr := &http2.Transport{AllowHTTP: true, StreamID: start, PriorityFrames: fingerprint.PriorityFrames,
								DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
									return (&net.Dialer{}).DialContext(ctx, network, addr)
								}}
							defer tr.CloseIdleConnections()
							transport = tr
						}
						ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
						defer cancel()
						req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+listener.Addr().String(), nil)
						response, err := transport.RoundTrip(req)
						if response != nil {
							response.Body.Close()
						}
						if code == http2.ErrCodeNo {
							if err != nil || response == nil || response.StatusCode != 200 || requests.Load() != 2 {
								t.Fatalf("graceful GOAWAY: response=%v err=%v requests=%d", response, err, requests.Load())
							}
						} else if err == nil || !strings.Contains(err.Error(), code.String()) || requests.Load() != 1 {
							t.Fatalf("GOAWAY error must not retry first request: err=%v requests=%d", err, requests.Load())
						}
					})
				}
			}
		}
	}
}

func TestFingerprintSingleUseFirstStream(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	transport := &http.Transport{Protocols: protocols, DisableKeepAlives: true,
		HTTP2: &http.HTTP2Config{Fingerprint: &http.HTTP2Fingerprint{StreamID: 3}}}
	defer transport.CloseIdleConnections()
	for range 2 {
		done := make(chan observedRequest, 1)
		go rawHTTP2Peer(listener, done)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+listener.Addr().String(), nil)
		response, err := transport.RoundTrip(req)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		cancel()
		if got := <-done; got.err != nil || got.streamID != 3 {
			t.Fatalf("single-use connection: %+v", got)
		}
	}
}
