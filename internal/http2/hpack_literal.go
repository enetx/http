package http2

import (
	"fmt"

	"golang.org/x/net/http2/hpack"
)

// Only literal framing is adapted. Table storage, eviction and Huffman codes
// continue to come from the official x/net codec.

// RFC 7541 4.4 empties the table on an oversized incremental insertion. The
// upstream encoder only attempts inserts that fit, so clear it through its
// public capacity API after emitting that literal. Real pending size updates
// have already been emitted by WriteField. Drain the reset's artificial updates
// into the private buffer with a non-inserting field, then discard those bytes;
// neither a reset notification nor the drain field is sent to the peer.
func (cc *ClientConn) clearFingerprintEncoderTable() error {
	capacity := cc.henc.MaxDynamicTableSize()
	if capacity == 0 {
		return nil
	}
	cc.henc.SetMaxDynamicTableSize(0)
	cc.henc.SetMaxDynamicTableSize(capacity)
	end := cc.hbuf.Len()
	err := cc.henc.WriteField(hpack.HeaderField{Name: ":method", Value: "GET", Sensitive: true})
	cc.hbuf.Truncate(end)
	if cc.fingerprintTable != nil {
		cc.fingerprintTable.resize(0)
		cc.fingerprintTable.resize(capacity)
	}
	return err
}

func readHPACKIndex(data []byte, prefix uint8) (uint64, error) {
	mask := uint64(1<<prefix) - 1
	index := uint64(data[0]) & mask
	if index < mask {
		return index, nil
	}
	for i, shift := 1, uint(0); i < len(data) && shift < 63; i, shift = i+1, shift+7 {
		index += uint64(data[i]&127) << shift
		if data[i]&128 == 0 {
			return index, nil
		}
	}
	return 0, fmt.Errorf("http2: invalid HPACK name index")
}

func appendHPACKIndex(dst []byte, prefix uint8, flags byte, index uint64) []byte {
	mask := uint64(1<<prefix) - 1
	if index < mask {
		return append(dst, flags|byte(index))
	}
	dst = append(dst, flags|byte(mask))
	index -= mask
	for index >= 128 {
		dst = append(dst, byte(index)|128)
		index >>= 7
	}
	return append(dst, byte(index))
}

func appendHPACKString(dst []byte, value string, mode HPACKHuffmanMode) []byte {
	n := hpack.HuffmanEncodeLength(value)
	if mode == HPACKHuffmanAlways || mode == HPACKHuffmanDefault && n < uint64(len(value)) {
		dst = appendHPACKIndex(dst, 7, 128, n)
		return hpack.AppendHuffmanString(dst, value)
	}
	dst = appendHPACKIndex(dst, 7, 0, uint64(len(value)))
	return append(dst, value...)
}
