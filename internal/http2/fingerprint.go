package http2

import (
	"fmt"
	"slices"
)

// Fingerprint controls the client's HTTP/2 connection and request representation.
// A nil Fingerprint retains the upstream defaults.
type Fingerprint struct {
	// StreamReceiveWindow increases client-created streams with an initial
	// WINDOW_UPDATE after HEADERS, independently of SETTINGS_INITIAL_WINDOW_SIZE.
	StreamReceiveWindow       uint32
	ReceiveWindowUpdatePolicy func(ReceiveWindowUpdateInfo) bool
	Settings                  []Setting
	StreamID                  uint32
	ConnectionFlow            uint32
	PriorityParam             PriorityParam
	PriorityFrames            []PriorityFrame
	HPACK                     *HPACKConfig
	HeaderPriority            func(PriorityRequest) PriorityParam
	ChainStreamDependencies   bool
}

// Clone freezes mutable configuration when a connection is created.
func (fp *Fingerprint) Clone() *Fingerprint {
	if fp == nil {
		return nil
	}
	copy := *fp
	copy.Settings = slices.Clone(fp.Settings)
	copy.PriorityFrames = slices.Clone(fp.PriorityFrames)
	copy.HPACK = fp.HPACK.Clone()
	return &copy
}

// firstRequestStreamID includes reserved PRIORITY streams, which advance the
// first request past the configured starting ID. Native connection lifecycle
// checks must use this value instead of assuming the first request is stream 1.
func (cc *ClientConn) firstRequestStreamID() uint32 {
	if fp := cc.fingerprint; fp != nil {
		if n := len(fp.PriorityFrames); n != 0 {
			return fp.PriorityFrames[n-1].StreamID + 2
		}
		if fp.StreamID != 0 {
			return fp.StreamID
		}
	}
	return 1
}

func (cc *ClientConn) applyFingerprint(conf *Config) error {
	fp := conf.Fingerprint
	if fp == nil {
		return nil
	}
	if err := fp.HPACK.Validate(); err != nil {
		return err
	}
	if fp.StreamID != 0 {
		if fp.StreamID%2 == 0 || fp.StreamID > 0x7fffffff {
			return fmt.Errorf("http2: invalid initial stream ID %d", fp.StreamID)
		}
		cc.nextStreamID = fp.StreamID
	}
	if fp.ConnectionFlow > 0x7fffffff {
		return fmt.Errorf("http2: invalid connection flow increment %d", fp.ConnectionFlow)
	}
	if fp.ConnectionFlow != 0 {
		conf.MaxReceiveBufferPerConnection = int(fp.ConnectionFlow)
	}
	seen := make(map[SettingID]bool, len(fp.Settings))
	for _, s := range fp.Settings {
		if seen[s.ID] {
			return fmt.Errorf("http2: duplicate initial setting %v", s.ID)
		}
		seen[s.ID] = true
		if err := s.Valid(); err != nil {
			return fmt.Errorf("http2: invalid initial setting %v: %w", s, err)
		}
		switch s.ID {
		case SettingInitialWindowSize:
			cc.initialStreamRecvWindowSize = int32(s.Val)
		case SettingHeaderTableSize:
			conf.MaxDecoderHeaderTableSize = int(s.Val)
		case SettingMaxFrameSize:
			conf.MaxReadFrameSize = int(s.Val)
		}
	}
	if fp.StreamReceiveWindow > 0x7fffffff || (fp.StreamReceiveWindow != 0 && fp.StreamReceiveWindow < uint32(cc.initialStreamRecvWindowSize)) {
		return fmt.Errorf("http2: stream receive window must not shrink the advertised initial window")
	}
	return nil
}

func (cc *ClientConn) initialFingerprintSettings(defaults []Setting) []Setting {
	if fp := cc.fingerprint; fp != nil && len(fp.Settings) > 0 {
		return fp.Settings
	}
	return defaults
}

func (cc *ClientConn) writeFingerprintPriorities() error {
	if cc.fingerprint == nil {
		return nil
	}
	for _, p := range cc.fingerprint.PriorityFrames {
		if p.StreamID == 0 || p.StreamID%2 == 0 || p.StreamID > 0x7ffffffd {
			return fmt.Errorf("http2: invalid priority stream ID %d", p.StreamID)
		}
		if err := cc.fr.WritePriority(p.StreamID, p.PriorityParam); err != nil {
			return err
		}
		cc.nextStreamID = p.StreamID + 2
	}
	return nil
}

func (cc *ClientConn) fingerprintPriority() PriorityParam {
	if cc.fingerprint != nil {
		return cc.fingerprint.PriorityParam
	}
	return PriorityParam{}
}

func (cc *ClientConn) fingerprintHeaderListSize(defaultSize uint32) uint32 {
	if cc.fingerprint != nil {
		for _, s := range cc.fingerprint.Settings {
			if s.ID == SettingMaxHeaderListSize && s.Val != 0 {
				return s.Val
			}
		}
	}
	return defaultSize
}
