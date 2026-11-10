package http2

import "time"

// ReceiveWindowUpdateInfo describes receive credit when the application returns
// consumed bytes. The policy runs under the connection lock, must return promptly
// and must be concurrency safe. It must not call back into the transport.
type ReceiveWindowUpdateInfo struct {
	Window, Available, Pending int32
	Elapsed                    time.Duration
}

type receiveWindowUpdateState struct {
	policy func(ReceiveWindowUpdateInfo) bool
	window int32
	last   time.Time
}

func (f *inflow) setUpdatePolicy(policy func(ReceiveWindowUpdateInfo) bool) {
	if policy != nil {
		f.refresh = &receiveWindowUpdateState{policy: policy, window: f.avail, last: time.Now()}
	}
}

func (f *inflow) deferWindowUpdate() bool {
	state := f.refresh
	now := time.Now()
	if !state.policy(ReceiveWindowUpdateInfo{Window: state.window, Available: f.avail, Pending: f.unsent, Elapsed: now.Sub(state.last)}) && f.avail > 0 {
		return true
	}
	state.last = now
	return false
}

func (cc *ClientConn) streamReceiveWindow() int32 {
	if fp := cc.fingerprint; fp != nil && fp.StreamReceiveWindow != 0 {
		return int32(fp.StreamReceiveWindow)
	}
	return cc.initialStreamRecvWindowSize
}

func (cc *ClientConn) writeInitialStreamWindow(stream uint32) error {
	if delta := cc.streamReceiveWindow() - cc.initialStreamRecvWindowSize; delta > 0 {
		if err := cc.fr.WriteWindowUpdate(stream, uint32(delta)); err != nil {
			return err
		}
		return cc.bw.Flush()
	}
	return nil
}
