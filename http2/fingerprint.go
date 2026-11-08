package http2

import "github.com/enetx/http"

func (t *Transport) fingerprint() *http.HTTP2Fingerprint {
	if len(t.Settings) == 0 && t.StreamID == 0 && t.ConnectionFlow == 0 && t.PriorityParam.IsZero() && len(t.PriorityFrames) == 0 {
		return nil
	}
	return &http.HTTP2Fingerprint{Settings: t.Settings, StreamID: t.StreamID, ConnectionFlow: t.ConnectionFlow, PriorityParam: t.PriorityParam, PriorityFrames: t.PriorityFrames}
}
