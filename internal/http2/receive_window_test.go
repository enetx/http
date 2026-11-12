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

func TestReceiveWindowPolicyOwnsExhaustedCredit(t *testing.T) {
	var flow inflow
	flow.init(100)
	flow.setUpdatePolicy(func(ReceiveWindowUpdateInfo) bool { return false })
	if !flow.take(100) || flow.add(1) != 0 {
		t.Fatal("exhausted peer credit must not override the explicit policy")
	}
	if flow.avail != 0 || flow.unsent != 1 {
		t.Fatalf("invalid credit: %+v", flow)
	}
	if flow.take(1) {
		t.Fatal("policy must not permit over-credit data")
	}
	if flow.add(99) != 0 || flow.avail != 0 || flow.unsent != 100 {
		t.Fatalf("an always-false policy must retain all returned credit: %+v", flow)
	}
}

func TestReceiveWindowPolicyStrictBatchingWithFullReadBuffer(t *testing.T) {
	// The peer can exhaust its window before the application drains accepted
	// DATA. Subsequent reads must still obey the policy's strict threshold.
	const window = 100
	var flow inflow
	flow.init(window)
	var snapshot ReceiveWindowUpdateInfo
	flow.setUpdatePolicy(func(info ReceiveWindowUpdateInfo) bool {
		snapshot = info
		return info.Pending > info.Window/2
	})
	for cycle := 0; cycle < 4; cycle++ {
		available := flow.avail
		if !flow.take(uint32(available)) || flow.avail != 0 {
			t.Fatal("peer must be able to fill its current window into the read buffer")
		}
		last := flow.refresh.last
		for _, read := range []int{19, 31} {
			if got := flow.add(read); got != 0 {
				t.Fatalf("cycle %d: premature update %d, snapshot %+v", cycle, got, snapshot)
			}
			if flow.avail != 0 || flow.unsent > window/2 || !flow.refresh.last.Equal(last) {
				t.Fatalf("cycle %d: deferred update changed credit or timestamp: %+v", cycle, flow)
			}
			if flow.take(1) {
				t.Fatal("buffered consumed credit is not peer credit before WINDOW_UPDATE")
			}
		}
		if snapshot.Available != 0 || snapshot.Pending != window/2 || snapshot.Window != window {
			t.Fatalf("cycle %d: incorrect exhausted-window snapshot: %+v", cycle, snapshot)
		}
		if got := flow.add(1); got != window/2+1 || flow.avail != window/2+1 || flow.unsent != 0 {
			t.Fatalf("cycle %d: threshold crossing did not release all pending credit: %d, %+v", cycle, got, flow)
		}
		if flow.refresh.last.Before(last) {
			t.Fatal("an emitted update cannot move its timestamp backwards")
		}
	}
}

func TestReceiveWindowPolicyNativeExhaustedCredit(t *testing.T) {
	var flow inflow
	flow.init(100)
	flow.setUpdatePolicy(nil)
	if flow.refresh != nil || !flow.take(100) || flow.add(1) != 1 {
		t.Fatal("nil policy must retain native exhausted-credit recovery")
	}
	if flow.avail != 1 || flow.unsent != 0 || flow.take(2) {
		t.Fatalf("invalid native credit: %+v", flow)
	}
}

func TestReceiveWindowPolicyExplicitEmergency(t *testing.T) {
	var flow inflow
	flow.init(100)
	flow.setUpdatePolicy(func(info ReceiveWindowUpdateInfo) bool {
		return info.Pending >= 80 || info.Available <= 10
	})
	if !flow.take(89) || flow.add(1) != 0 {
		t.Fatal("credit above the explicit emergency threshold must remain batched")
	}
	if !flow.take(1) || flow.add(1) != 2 || flow.avail != 12 || flow.unsent != 0 {
		t.Fatalf("explicit emergency policy must release pending credit: %+v", flow)
	}
	if !flow.take(12) || flow.add(1) != 1 || flow.avail != 1 || flow.unsent != 0 {
		t.Fatalf("explicit emergency policy must also work at zero peer credit: %+v", flow)
	}
}

func TestReceiveWindowPolicyPreservesOverflowGuard(t *testing.T) {
	var flow inflow
	flow.init(100)
	flow.setUpdatePolicy(func(ReceiveWindowUpdateInfo) bool { return false })
	if !flow.take(100) || flow.add(100) != 0 {
		t.Fatal("policy must be able to defer a full window")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("deferred credit must still reject an update above the maximum window")
		}
	}()
	flow.add(maxFlowWindow)
}
