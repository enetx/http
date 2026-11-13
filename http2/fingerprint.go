package http2

import (
	"github.com/enetx/http"
	native "github.com/enetx/http/internal/http2"
)

type HPACKConfig = native.HPACKConfig
type HPACKRepresentation = native.HPACKRepresentation
type HPACKHuffmanMode = native.HPACKHuffmanMode
type HPACKNameReferencePolicy = native.HPACKNameReferencePolicy
type PriorityRequest = native.PriorityRequest
type ReceiveWindowUpdateInfo = native.ReceiveWindowUpdateInfo
type ConnectionErrorInfo = native.ConnectionErrorInfo

const (
	HPACKDefault              = native.HPACKDefault
	HPACKWithoutIndexing      = native.HPACKWithoutIndexing
	HPACKNeverIndex           = native.HPACKNeverIndex
	HPACKIncrementalIndexing  = native.HPACKIncrementalIndexing
	HPACKHuffmanDefault       = native.HPACKHuffmanDefault
	HPACKHuffmanAlways        = native.HPACKHuffmanAlways
	HPACKHuffmanNever         = native.HPACKHuffmanNever
	HPACKNameReferenceDefault = native.HPACKNameReferenceDefault
	HPACKNameReferenceLast    = native.HPACKNameReferenceLast
	HPACKNameReferenceFirst   = native.HPACKNameReferenceFirst
)

func (t *Transport) fingerprint() *http.HTTP2Fingerprint {
	if len(t.Settings) == 0 && t.StreamID == 0 && t.ConnectionFlow == 0 && t.PriorityParam.IsZero() && len(t.PriorityFrames) == 0 && t.HPACK == nil && t.HeaderPriority == nil && !t.ChainStreamDependencies && t.StreamReceiveWindow == 0 && t.ReceiveWindowUpdatePolicy == nil && t.ConnectionErrorPolicy == nil {
		return nil
	}
	return (&http.HTTP2Fingerprint{Settings: t.Settings, StreamID: t.StreamID, ConnectionFlow: t.ConnectionFlow, PriorityParam: t.PriorityParam, PriorityFrames: t.PriorityFrames, HPACK: t.HPACK, HeaderPriority: t.HeaderPriority, ChainStreamDependencies: t.ChainStreamDependencies, StreamReceiveWindow: t.StreamReceiveWindow, ReceiveWindowUpdatePolicy: t.ReceiveWindowUpdatePolicy, ConnectionErrorPolicy: t.ConnectionErrorPolicy}).Clone()
}
