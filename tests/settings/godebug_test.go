package godebug_test

import (
	"testing"

	. "github.com/enetx/http/internal/godebug"
)

func TestCurrentEnvironmentAndLastSettingWins(t *testing.T) {
	setting := New("limit")
	t.Setenv("GODEBUG", "other=3,limit=10,limit=20")
	if got := setting.Value(); got != "20" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("GODEBUG", "other=3")
	if got := setting.Value(); got != "" {
		t.Fatalf("stale value %q", got)
	}
}
