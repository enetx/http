package http2

import (
	"fmt"
	"slices"
)

// Fingerprint controls the client's HTTP/2 connection and request representation.
// A nil Fingerprint retains the upstream defaults.
type Fingerprint struct {
	Settings                []Setting
	StreamID                uint32
	ConnectionFlow          uint32
	PriorityParam           PriorityParam
	PriorityFrames          []PriorityFrame
	HPACK                   *HPACKConfig
	HeaderPriority          func(PriorityRequest) PriorityParam
	ChainStreamDependencies bool
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
