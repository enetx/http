package http2_test

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enetx/http"
	"github.com/enetx/http/http2"
)

func TestConnectionErrorPolicyOnWire(t *testing.T) {
	for _, facade := range []bool{false, true} {
		for _, goaway := range []bool{false, true} {
			for _, promote := range []bool{false, true} {
				t.Run(fmt.Sprintf("facade=%v/goaway=%v/promote=%v", facade, goaway, promote), func(t *testing.T) {
					listener, err := net.Listen("tcp4", "127.0.0.1:0")
					if err != nil {
						t.Fatal(err)
					}
					defer listener.Close()
					peer := make(chan error, 1)
					go func() { peer <- servePolicyError(listener, goaway, promote) }()
					var calls atomic.Int32
					policy := func(info http2.ConnectionErrorInfo) bool {
						calls.Add(1)
						want := http2.FrameRSTStream
						if goaway {
							want = http2.FrameGoAway
						}
						if info.Conn == nil || info.Code != http2.ErrCodeHTTP11Required || info.FrameType != want {
							t.Errorf("invalid policy event: %+v", info)
						}
						return true
					}
					protocols := new(http.Protocols)
					protocols.SetUnencryptedHTTP2(true)
					base := &http.Transport{Protocols: protocols, MaxConnsPerHost: 1}
					defer base.CloseIdleConnections()
					var transport http.RoundTripper = base
					if facade {
						two, err := http2.ConfigureTransports(base)
						if err != nil {
							t.Fatal(err)
						}
						two.AllowHTTP = true
						two.DialTLSContext = func(ctx context.Context, network, address string, _ *tls.Config) (net.Conn, error) {
							return (&net.Dialer{}).DialContext(ctx, network, address)
						}
						if promote {
							two.ConnectionErrorPolicy = policy
						}
						transport = two
					} else if promote {
						base.HTTP2 = &http.HTTP2Config{Fingerprint: &http.HTTP2Fingerprint{ConnectionErrorPolicy: policy}}
					}
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					results := make(chan error, 2)
					for range 2 {
						go func() {
							req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+listener.Addr().String(), nil)
							response, err := transport.RoundTrip(req)
							if response != nil {
								_, _ = io.Copy(io.Discard, response.Body)
								response.Body.Close()
							}
							results <- err
						}()
					}
					failures := 0
					for range 2 {
						if err := <-results; err != nil {
							failures++
							if !strings.Contains(err.Error(), "HTTP_1_1_REQUIRED") {
								t.Errorf("lost peer error: %v", err)
							}
						}
					}
					base.CloseIdleConnections()
					if err := <-peer; err != nil {
						t.Fatal(err)
					}
					want := 1
					if goaway {
						want = 0
					}
					if promote {
						want = 2
					}
					if failures != want {
						t.Errorf("failed requests=%d want=%d", failures, want)
					}
					if promote && calls.Load() != 1 {
						t.Errorf("policy calls=%d", calls.Load())
					}
				})
			}
		}
	}
}

func servePolicyError(listener net.Listener, goaway, promote bool) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(conn, preface); err != nil {
		return err
	}
	fr := http2.NewFramer(conn, conn)
	if err := fr.WriteSettings(); err != nil {
		return err
	}
	var streams []uint32
	for len(streams) < 2 {
		frame, err := fr.ReadFrame()
		if err != nil {
			return err
		}
		switch f := frame.(type) {
		case *http2.SettingsFrame:
			if !f.IsAck() {
				if err := fr.WriteSettingsAck(); err != nil {
					return err
				}
			}
		case *http2.HeadersFrame:
			streams = append(streams, f.StreamID)
		}
	}
	if goaway {
		err = fr.WriteGoAway(streams[1], http2.ErrCodeHTTP11Required, nil)
	} else {
		err = fr.WriteRSTStream(streams[0], http2.ErrCodeHTTP11Required)
	}
	if err != nil {
		return err
	}
	if !promote {
		for i, id := range streams {
			if !goaway && i == 0 {
				continue
			}
			if err := fr.WriteHeaders(http2.HeadersFrameParam{StreamID: id, EndHeaders: true, EndStream: true, BlockFragment: []byte{0x88}}); err != nil {
				return err
			}
		}
	}
	for {
		frame, err := fr.ReadFrame()
		if err != nil {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				return fmt.Errorf("peer connection was not closed: %w", err)
			}
			return nil
		}
		if _, ok := frame.(*http2.GoAwayFrame); ok {
			return errors.New("policy shutdown must not send GOAWAY")
		}
	}
}
