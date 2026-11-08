package http

import "github.com/enetx/http/internal/http2"

// HTTP2Fingerprint controls request stream IDs and the initial SETTINGS,
// PRIORITY and WINDOW_UPDATE frames. A nil value uses upstream defaults.
type HTTP2Fingerprint = http2.Fingerprint

type HTTP2Setting = http2.Setting
type HTTP2PriorityParam = http2.PriorityParam
type HTTP2PriorityFrame = http2.PriorityFrame
