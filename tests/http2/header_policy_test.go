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
	"golang.org/x/net/http2/hpack"
)

type policyCapture struct {
	id       uint32
	priority http2.PriorityParam
	flags    http2.Flags
	fields   []hpack.HeaderField
	forms    []byte
	block    []byte
	err      error
}

// Decode a byte at a time to record the actual representation opcode of each
// emitted field, including name references to the connection's dynamic table.
func decodePolicyBlock(dec *hpack.Decoder, block []byte) ([]hpack.HeaderField, []byte, error) {
	var fields []hpack.HeaderField
	var forms []byte
	for len(block) > 0 {
		if block[0]&0xe0 == 0x20 {
			n := 1
			if block[0]&31 == 31 {
				for n < len(block) {
					b := block[n]
					n++
					if b&128 == 0 {
						break
					}
				}
			}
			if _, err := dec.Write(block[:n]); err != nil {
				return nil, nil, err
			}
			block = block[n:]
			continue
		}
		first := block[0]
		emitted := false
		dec.SetEmitFunc(func(f hpack.HeaderField) { emitted = true; fields = append(fields, f); forms = append(forms, first) })
		for !emitted {
			if len(block) == 0 {
				return nil, nil, io.ErrUnexpectedEOF
			}
			if _, err := dec.Write(block[:1]); err != nil {
				return nil, nil, err
			}
			block = block[1:]
		}
	}
	return fields, forms, dec.Close()
}

func newPolicyTransport(t *testing.T, count int, hold bool, configure func(*http2.Transport)) (*http2.Transport, <-chan policyCapture) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	events := make(chan policyCapture, count+1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			events <- policyCapture{err: err}
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		preface := make([]byte, len(http2.ClientPreface))
		if _, err := io.ReadFull(conn, preface); err != nil {
			events <- policyCapture{err: err}
			return
		}
		fr := http2.NewFramer(conn, conn)
		if err := fr.WriteSettings(http2.Setting{ID: http2.SettingHeaderTableSize, Val: 256}); err != nil {
			events <- policyCapture{err: err}
			return
		}
		dec := hpack.NewDecoder(4096, nil)
		var pending []uint32
		var block []byte
		var got policyCapture
		for len(pending) < count {
			frame, err := fr.ReadFrame()
			if err != nil {
				events <- policyCapture{err: err}
				return
			}
			switch f := frame.(type) {
			case *http2.SettingsFrame:
				if !f.IsAck() {
					if err := fr.WriteSettingsAck(); err != nil {
						events <- policyCapture{err: err}
						return
					}
				}
				continue
			case *http2.HeadersFrame:
				got = policyCapture{id: f.StreamID, priority: f.Priority, flags: f.Flags}
				block = append(block[:0], f.HeaderBlockFragment()...)
				if !f.HeadersEnded() {
					continue
				}
			case *http2.ContinuationFrame:
				block = append(block, f.HeaderBlockFragment()...)
				if !f.HeadersEnded() {
					continue
				}
			default:
				continue
			}
			got.block = bytes.Clone(block)
			got.fields, got.forms, got.err = decodePolicyBlock(dec, block)
			events <- got
			if got.err != nil {
				return
			}
			pending = append(pending, got.id)
			if hold && len(pending) < count {
				continue
			}
			ids := pending
			if !hold {
				ids = pending[len(pending)-1:]
			}
			seenIDs := make(map[uint32]bool)
			for _, id := range ids {
				if seenIDs[id] {
					continue
				}
				seenIDs[id] = true
				if err := fr.WriteHeaders(http2.HeadersFrameParam{StreamID: id, BlockFragment: []byte{0x88}, EndHeaders: true, EndStream: true}); err != nil {
					events <- policyCapture{err: err}
					return
				}
			}
		}
	}()
	tr := &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, ln.Addr().String())
	}}
	configure(tr)
	t.Cleanup(tr.CloseIdleConnections)
	return tr, events
}

func nextPolicyCapture(t *testing.T, events <-chan policyCapture) policyCapture {
	t.Helper()
	select {
	case got := <-events:
		if got.err != nil {
			t.Fatal(got.err)
		}
		return got
	case <-time.After(6 * time.Second):
		t.Fatal("missing HTTP/2 policy capture")
	}
	return policyCapture{}
}

func TestHPACKRepresentationsAcrossRequests(t *testing.T) {
	for _, rep := range []http2.HPACKRepresentation{http2.HPACKDefault, http2.HPACKWithoutIndexing, http2.HPACKNeverIndex} {
		t.Run(fmt.Sprint(rep), func(t *testing.T) {
			tr, events := newPolicyTransport(t, 3, false, func(tr *http2.Transport) {
				tr.HPACK = &http2.HPACKConfig{Representations: map[string]http2.HPACKRepresentation{"x-repeat": rep, "authorization": http2.HPACKNeverIndex, ":method": http2.HPACKNeverIndex}}
			})
			for i := 0; i < 3; i++ {
				req, _ := http.NewRequest("GET", "http://example.test/", nil)
				req.Header.Set("X-Repeat", "constant")
				req.Header.Set("Authorization", "secret")
				resp, err := tr.RoundTrip(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				got := nextPolicyCapture(t, events)
				for j, field := range got.fields {
					switch field.Name {
					case ":method", "authorization":
						if !field.Sensitive || got.forms[j]&0xf0 != 0x10 {
							t.Fatalf("%s not never-indexed: %x", field.Name, got.forms[j])
						}
					case "x-repeat":
						form := got.forms[j]
						switch rep {
						case http2.HPACKDefault:
							if i == 0 && form&0xc0 != 0x40 || i > 0 && form&0x80 == 0 {
								t.Fatalf("request %d default form=%x", i, form)
							}
						case http2.HPACKWithoutIndexing:
							if form&0xf0 != 0 || field.Sensitive {
								t.Fatalf("without form=%x sensitive=%v", form, field.Sensitive)
							}
						case http2.HPACKNeverIndex:
							if form&0xf0 != 0x10 || !field.Sensitive {
								t.Fatalf("never form=%x", form)
							}
						}
					}
				}
			}
		})
	}
}

func TestHTTP2PriorityCallbackAndOpenStreamChain(t *testing.T) {
	tr, events := newPolicyTransport(t, 3, true, func(tr *http2.Transport) {
		tr.ChainStreamDependencies = true
		tr.HeaderPriority = func(req http2.PriorityRequest) http2.PriorityParam {
			weight := uint8(255)
			if req.URL.Path == "/low" {
				weight = 100
			}
			return http2.PriorityParam{Exclusive: true, Weight: weight}
		}
	})
	done := make(chan error, 3)
	var captures []policyCapture
	for _, path := range []string{"/high", "/low", "/high"} {
		go func() {
			req, _ := http.NewRequest("GET", "http://example.test"+path, nil)
			resp, err := tr.RoundTrip(req)
			if err == nil {
				resp.Body.Close()
			}
			done <- err
		}()
		captures = append(captures, nextPolicyCapture(t, events))
	}
	want := []http2.PriorityParam{{Exclusive: true, Weight: 255}, {StreamDep: 1, Exclusive: true, Weight: 100}, {StreamDep: 1, Exclusive: true, Weight: 255}}
	for i, got := range captures {
		if !reflect.DeepEqual(got.priority, want[i]) {
			t.Fatalf("stream %d priority=%+v want=%+v", got.id, got.priority, want[i])
		}
	}
	for range 3 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestHPACKValidationAndSnapshot(t *testing.T) {
	config := &http2.HPACKConfig{Representations: map[string]http2.HPACKRepresentation{"x-name": http2.HPACKNeverIndex}}
	copy := config.Clone()
	config.Representations["x-name"] = http2.HPACKDefault
	if copy.Representations["x-name"] != http2.HPACKNeverIndex {
		t.Fatal("mutable snapshot")
	}
	for _, bad := range []*http2.HPACKConfig{{Default: 99}, {Huffman: 99}, {NameReferencePolicy: 99}, {Representations: map[string]http2.HPACKRepresentation{"Bad": 0}}, {Representations: map[string]http2.HPACKRepresentation{"x": 99}}} {
		if bad.Validate() == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestHTTP2TrailersHaveHeaderPolicyWithoutRequestPriority(t *testing.T) {
	tr, events := newPolicyTransport(t, 2, true, func(tr *http2.Transport) {
		tr.PriorityParam = http2.PriorityParam{Exclusive: true, Weight: 200}
		tr.HPACK = &http2.HPACKConfig{Representations: map[string]http2.HPACKRepresentation{"x-trailer": http2.HPACKNeverIndex}}
	})
	req, _ := http.NewRequest("POST", "http://example.test/", strings.NewReader("body"))
	req.Trailer = http.Header{"X-Trailer": []string{"secret"}}
	response, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	first, trailers := nextPolicyCapture(t, events), nextPolicyCapture(t, events)
	if !first.flags.Has(http2.FlagHeadersPriority) {
		t.Fatal("request missing priority")
	}
	if trailers.flags.Has(http2.FlagHeadersPriority) {
		t.Fatal("request priority leaked onto trailers")
	}
	if len(trailers.fields) != 1 || trailers.fields[0] != (hpack.HeaderField{Name: "x-trailer", Value: "secret", Sensitive: true}) {
		t.Fatalf("trailers=%+v", trailers.fields)
	}
}

func TestHTTP2InvalidHeaderPolicyFailsPromptly(t *testing.T) {
	tr, _ := newPolicyTransport(t, 1, false, func(tr *http2.Transport) {
		tr.HPACK = &http2.HPACKConfig{Policy: func(string, string) http2.HPACKRepresentation { return 99 }}
	})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://example.test/", nil)
	_, err := tr.RoundTrip(req)
	if err == nil || !strings.Contains(err.Error(), "HPACK") {
		t.Fatalf("error=%v", err)
	}
}
