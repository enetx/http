package http2

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/net/http2/hpack"
)

func TestHPACKFirstStaticAndNewestDynamicNames(t *testing.T) {
	cc := &ClientConn{fingerprint: &Fingerprint{HPACK: &HPACKConfig{NameReferencePolicy: HPACKNameReferenceFirst}}}
	cc.henc = hpack.NewEncoder(&cc.hbuf)
	decoder := hpack.NewDecoder(4096, nil)
	for _, tc := range []struct {
		field hpack.HeaderField
		index uint64
	}{
		{hpack.HeaderField{Name: ":method", Value: "PATCH"}, 2},
		{hpack.HeaderField{Name: ":path", Value: "/varying"}, 4},
		{hpack.HeaderField{Name: ":scheme", Value: "other"}, 6},
		{hpack.HeaderField{Name: ":status", Value: "418"}, 8},
		{hpack.HeaderField{Name: "x-name", Value: "A"}, 0},
		{hpack.HeaderField{Name: "x-name", Value: "B"}, 62},
		{hpack.HeaderField{Name: "x-name", Value: "C"}, 62},
	} {
		cc.hbuf.Reset()
		if err := cc.writeFingerprintHeader(tc.field.Name, tc.field.Value); err != nil {
			t.Fatal(err)
		}
		data := cc.hbuf.Bytes()
		index, err := readHPACKIndex(data, 6)
		if err != nil || index != tc.index {
			t.Fatalf("%s name index=%d, want=%d, wire=%x: %v", tc.field.Name, index, tc.index, data, err)
		}
		fields, err := decoder.DecodeFull(data)
		if err != nil || len(fields) != 1 || fields[0] != tc.field {
			t.Fatalf("decoded=%+v, want=%+v: %v", fields, tc.field, err)
		}
	}
	// Indexed exact matches retain their full static indexes, regardless of
	// which name reference a literal would have used.
	cc.hbuf.Reset()
	if err := cc.writeFingerprintHeader(":path", "/index.html"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cc.hbuf.Bytes(), []byte{0x85}) {
		t.Fatalf("static exact match changed: %x", cc.hbuf.Bytes())
	}
	if cc.fingerprintTable != nil {
		t.Fatal("first-static lookup should not allocate a per-connection shadow table")
	}
}

func TestHPACKOversizedIncrementalInsertion(t *testing.T) {
	for _, namePolicy := range []HPACKNameReferencePolicy{HPACKNameReferenceDefault, HPACKNameReferenceFirst, HPACKNameReferenceLast} {
		for _, mode := range []HPACKHuffmanMode{HPACKHuffmanDefault, HPACKHuffmanAlways, HPACKHuffmanNever} {
			t.Run(fmt.Sprintf("names=%d/huffman=%d", namePolicy, mode), func(t *testing.T) {
				cc := &ClientConn{fingerprint: &Fingerprint{HPACK: &HPACKConfig{
					Default: HPACKIncrementalIndexing, Huffman: mode, NameReferencePolicy: namePolicy,
					IndexingLimit: func(uint32) uint32 { return 1 << 20 },
				}}}
				cc.henc = hpack.NewEncoder(&cc.hbuf)
				cc.henc.SetMaxDynamicTableSizeLimit(65536)
				decoder := hpack.NewDecoder(65536, nil)
				write := func(name, value string, updates []byte, indexed bool, index uint64) {
					t.Helper()
					cc.hbuf.Reset()
					if err := cc.writeFingerprintHeader(name, value); err != nil {
						t.Fatal(err)
					}
					data := cc.hbuf.Bytes()
					fieldBytes := skipHPACKSizeUpdates(data)
					if !bytes.Equal(data[:len(data)-len(fieldBytes)], updates) {
						t.Fatalf("unexpected table updates: wire=%x, want prefix=%x", data, updates)
					}
					prefix := uint8(6)
					if indexed {
						prefix = 7
						if fieldBytes[0]&0x80 == 0 {
							t.Fatalf("missing exact reference: %x", fieldBytes)
						}
					} else if fieldBytes[0]&0xc0 != 0x40 {
						t.Fatalf("missing incremental literal: %x", fieldBytes)
					}
					got, err := readHPACKIndex(fieldBytes, prefix)
					if err != nil || got != index {
						t.Fatalf("index=%d, want=%d: %v", got, index, err)
					}
					fields, err := decoder.DecodeFull(data)
					want := hpack.HeaderField{Name: name, Value: value}
					if err != nil || len(fields) != 1 || fields[0] != want {
						t.Fatalf("decode=%+v, want=%+v: %v", fields, want, err)
					}
				}
				cc.setFingerprintEncoderTableSize(128)
				write("x-old", "one", []byte{0x3f, 97}, false, 0)
				write("x-other", "two", nil, false, 0)
				// Preserve the real size update; the oversized field refers to
				// the surviving entry before its insertion clears the whole table.
				cc.setFingerprintEncoderTableSize(64)
				write("x-other", strings.Repeat("z", 160), []byte{0x3f, 33}, false, 62)
				if cc.fingerprintTable.size != 0 || len(cc.fingerprintTable.counts) != 0 {
					t.Fatal("oversized insertion left stale shadow entries")
				}
				write("x-other", "two", nil, false, 0)
				write("x-other", "two", nil, true, 62)
				write(":method", "GET", nil, true, 2)
				cc.setFingerprintEncoderTableSize(128)
				boundary := strings.Repeat("z", 89) // 32 + len("x-bound") + 89 = 128.
				write("x-bound", boundary, []byte{0x3f, 97}, false, 0)
				write("x-bound", boundary, nil, true, 62)
				write("x-bound", boundary+"z", nil, false, 62)
				write("x-other", "two", nil, false, 0)
				// A pending shrink and growth are real peer updates. The private
				// encoder reset must neither add to nor swallow this prefix.
				cc.setFingerprintEncoderTableSize(0)
				cc.setFingerprintEncoderTableSize(128)
				write("x-other", strings.Repeat("z", 160), []byte{0x20, 0x3f, 97}, false, 0)
				write("x-other", "two", nil, false, 0)
				cc.setFingerprintEncoderTableSize(0)
				write("x-other", "two", []byte{0x20}, false, 0)
				write("x-other", "two", nil, false, 0)
				cc.setFingerprintEncoderTableSize(128)
				write("x-other", "two", []byte{0x3f, 97}, false, 0)
				// The internal clear must retain the encoder's configured upper
				// bound: entries above Go's default 4096 still fit after growth.
				cc.setFingerprintEncoderTableSize(65536)
				large := strings.Repeat("z", 8192)
				write("x-large", large, []byte{0x3f, 0xe1, 0xff, 3}, false, 0)
				write("x-large", large, nil, true, 62)
			})
		}
	}
}

func TestHPACKIncrementalStaticMatchPreservesDynamicTable(t *testing.T) {
	cc := &ClientConn{fingerprint: &Fingerprint{HPACK: &HPACKConfig{Default: HPACKIncrementalIndexing}}}
	cc.henc = hpack.NewEncoder(&cc.hbuf)
	cc.setFingerprintEncoderTableSize(48)
	decoder := hpack.NewDecoder(4096, nil)
	for i, field := range []hpack.HeaderField{
		{Name: "x", Value: "v"},                           // 34 bytes: fits the small dynamic table.
		{Name: "accept-encoding", Value: "gzip, deflate"}, // 60 bytes, but static index 16 needs no insertion.
		{Name: "x", Value: "v"},
	} {
		cc.hbuf.Reset()
		if err := cc.writeFingerprintHeader(field.Name, field.Value); err != nil {
			t.Fatal(err)
		}
		data := cc.hbuf.Bytes()
		if i == 2 && !bytes.Equal(data, []byte{0xbe}) {
			t.Fatalf("static exact match evicted a dynamic entry: %x", data)
		}
		fields, err := decoder.DecodeFull(data)
		if err != nil || len(fields) != 1 || fields[0] != field {
			t.Fatalf("decode=%+v want=%+v: %v", fields, field, err)
		}
	}
}

func TestHPACKWithoutPreservesTableUpdatesAndDynamicNameReferences(t *testing.T) {
	cc := &ClientConn{fingerprint: &Fingerprint{HPACK: &HPACKConfig{Default: HPACKWithoutIndexing}}}
	cc.henc = hpack.NewEncoder(&cc.hbuf)
	cc.henc.SetMaxDynamicTableSize(32)
	cc.henc.SetMaxDynamicTableSize(128)
	if err := cc.writeFingerprintHeader("x-first", "value"); err != nil {
		t.Fatal(err)
	}
	data := cc.hbuf.Bytes()
	if len(data) < 5 || data[0] != 0x3f || data[1] != 1 || data[2] != 0x3f || data[3] != 97 || data[4] != 0 {
		t.Fatalf("size updates or literal prefix changed: %x", data)
	}
	decoder := hpack.NewDecoder(4096, nil)
	fields, err := decoder.DecodeFull(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0].Sensitive {
		t.Fatalf("fields=%+v", fields)
	}
	cc.hbuf.Reset()
	cc.henc.SetMaxDynamicTableSize(4096)
	cc.fingerprint.HPACK.Default = HPACKDefault
	for i := range 40 {
		if err := cc.writeFingerprintHeader(fmt.Sprintf("x-field-%d", i), "original"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := decoder.DecodeFull(cc.hbuf.Bytes()); err != nil {
		t.Fatal(err)
	}
	cc.hbuf.Reset()
	cc.fingerprint.HPACK.Default = HPACKWithoutIndexing
	if err := cc.writeFingerprintHeader("x-field-0", "changed"); err != nil {
		t.Fatal(err)
	}
	if cc.hbuf.Bytes()[0] != 15 {
		t.Fatalf("long name index opcode=%x", cc.hbuf.Bytes())
	}
	fields, err = decoder.DecodeFull(cc.hbuf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0] != (hpack.HeaderField{Name: "x-field-0", Value: "changed"}) {
		t.Fatalf("fields=%+v", fields)
	}
}

func TestHPACKInsertionLimitPreservesExactMatchesAndEviction(t *testing.T) {
	cc := &ClientConn{fingerprint: &Fingerprint{HPACK: &HPACKConfig{IndexingLimit: func(capacity uint32) uint32 {
		if capacity < 128 {
			return 0
		}
		return capacity / 2
	}}}}
	cc.henc = hpack.NewEncoder(&cc.hbuf)
	decoder := hpack.NewDecoder(4096, nil)
	write := func(name, value string) byte {
		t.Helper()
		cc.hbuf.Reset()
		if err := cc.writeFingerprintHeader(name, value); err != nil {
			t.Fatal(err)
		}
		data := cc.hbuf.Bytes()
		form := skipHPACKSizeUpdates(data)[0]
		if _, err := decoder.DecodeFull(data); err != nil {
			t.Fatal(err)
		}
		return form
	}
	resize := func(size uint32) {
		cc.henc.SetMaxDynamicTableSize(size)
		if cc.fingerprintTable != nil {
			cc.fingerprintTable.resize(cc.henc.MaxDynamicTableSize())
		}
	}
	if got := write("x-entry", "123456789012345678901234567890123456789012345678901234567890"); got&0xc0 != 0x40 {
		t.Fatalf("first insert=%x", got)
	}
	resize(128) // Entry still fits, but is now larger than half the table.
	if got := write("x-entry", "123456789012345678901234567890123456789012345678901234567890"); got&0x80 == 0 {
		t.Fatalf("existing entry was not reused: %x", got)
	}
	if got := write("x-other", "123456789012345678901234567890123456789012345678901234567890"); got&0xf0 != 0 {
		t.Fatalf("oversize entry inserted: %x", got)
	}
	resize(0)
	resize(128) // Must evict even if no fields are encoded between updates.
	if got := write("x-entry", "123456789012345678901234567890123456789012345678901234567890"); got&0xf0 != 0 {
		t.Fatalf("stale membership survived eviction: %x", got)
	}
	resize(64)
	if got := write(":method", "GET"); got&0x80 == 0 {
		t.Fatalf("static entry lost: %x", got)
	}
	if got := write("x-small", "a"); got&0xf0 != 0 {
		t.Fatalf("small table allowed insertion: %x", got)
	}
}

func TestHPACKLiteralNameSelectionAndHuffmanModes(t *testing.T) {
	for _, mode := range []HPACKHuffmanMode{HPACKHuffmanDefault, HPACKHuffmanAlways, HPACKHuffmanNever} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			cc := &ClientConn{fingerprint: &Fingerprint{HPACK: &HPACKConfig{Huffman: mode, NameReferencePolicy: HPACKNameReferenceLast, Representations: map[string]HPACKRepresentation{":path": HPACKWithoutIndexing, "authorization": HPACKNeverIndex}}}}
			cc.henc = hpack.NewEncoder(&cc.hbuf)
			decoder := hpack.NewDecoder(4096, nil)
			for i, field := range []hpack.HeaderField{{Name: ":path", Value: "/index.html"}, {Name: "x-name", Value: "A"}, {Name: "x-name", Value: "B"}, {Name: "x-name", Value: "C"}, {Name: "authorization", Value: "token"}} {
				cc.hbuf.Reset()
				if err := cc.writeFingerprintHeader(field.Name, field.Value); err != nil {
					t.Fatal(err)
				}
				data := cc.hbuf.Bytes()
				if i == 0 && data[0] != 5 {
					t.Fatalf("static exact name must reference index 5: %x", data)
				}
				if i == 3 && (data[0] != 0x7f || data[1] != 0) {
					t.Fatalf("expected oldest dynamic name index 63: %x", data)
				}
				if i == 1 {
					if mode == HPACKHuffmanAlways && data[len(data)-2]&128 == 0 {
						t.Fatalf("one-byte value was not Huffman encoded: %x", data)
					}
					if mode == HPACKHuffmanNever && data[1]&128 != 0 {
						t.Fatalf("name Huffman flag set: %x", data)
					}
				}
				fields, err := decoder.DecodeFull(data)
				if err != nil {
					t.Fatal(err)
				}
				field.Sensitive = field.Name == "authorization"
				if len(fields) != 1 || fields[0] != field {
					t.Fatalf("decoded=%+v want=%+v", fields, field)
				}
			}
		})
	}
}

func TestHPACKTableSizeUpdateDeduplication(t *testing.T) {
	cc := &ClientConn{fingerprint: &Fingerprint{HPACK: &HPACKConfig{DeduplicateTableSizeUpdates: true}}}
	cc.henc = hpack.NewEncoder(&cc.hbuf)
	cc.setFingerprintEncoderTableSize(4096)
	if err := cc.writeFingerprintHeader(":method", "GET"); err != nil {
		t.Fatal(err)
	}
	if got := cc.hbuf.Bytes(); len(got) != 1 || got[0] != 0x82 {
		t.Fatalf("redundant table update: %x", got)
	}
	cc.hbuf.Reset()
	cc.setFingerprintEncoderTableSize(0)
	cc.setFingerprintEncoderTableSize(4096)
	cc.setFingerprintEncoderTableSize(4096)
	if err := cc.writeFingerprintHeader(":method", "GET"); err != nil {
		t.Fatal(err)
	}
	data := cc.hbuf.Bytes()
	if len(data) < 3 || data[0] != 0x20 || data[1] != 0x3f {
		t.Fatalf("pending shrink/grow updates lost: %x", data)
	}
	decoder := hpack.NewDecoder(4096, nil)
	if _, err := decoder.DecodeFull(data); err != nil {
		t.Fatal(err)
	}
}
