package http2

import (
	"testing"
	"time"
)

func TestReceiveWindowPolicyBatching(t *testing.T) {
	var flow inflow
	flow.init(100)
	flow.setUpdatePolicy(func(info ReceiveWindowUpdateInfo) bool {
		if info.Window != 100 {
			t.Errorf("window=%d", info.Window)
		}
		return info.Pending > info.Window/2 || info.Elapsed >= 5*time.Second
	})
	if !flow.take(50) || flow.add(50) != 0 {
		t.Fatal("half-window must remain batched")
	}
	if !flow.take(1) || flow.add(1) != 51 {
		t.Fatal("half-window plus one must refresh all pending credit")
	}
	flow.refresh.last = time.Now().Add(-6 * time.Second)
	if !flow.take(1) || flow.add(1) != 1 {
		t.Fatal("elapsed policy must flush small credit")
	}
}

func TestReceiveWindowPolicyCannotStallExhaustedCredit(t *testing.T) {
	var flow inflow
	flow.init(100)
	flow.setUpdatePolicy(func(ReceiveWindowUpdateInfo) bool { return false })
	if !flow.take(100) || flow.add(1) != 1 {
		t.Fatal("exhausted credit must refresh despite policy")
	}
	if flow.avail != 1 || flow.unsent != 0 {
		t.Fatalf("invalid credit: %+v", flow)
	}
	if flow.take(2) {
		t.Fatal("policy must not permit over-credit data")
	}
}
