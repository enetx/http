package headerorder_test

import (
	"reflect"
	"testing"

	. "github.com/enetx/http/internal/headerorder"
)

func TestSorted(t *testing.T) {
	h := map[string][]string{"X-Z": {"z1", "z2"}, "X-A": {"a"}, "X-M": {"m"}, HeaderOrderKey: {"x-z"}, PHeaderOrderKey: {":method"}}
	for _, tc := range []struct {
		name    string
		order   []string
		exclude map[string]bool
		want    []string
	}{
		{name: "default", want: []string{"X-A", "X-M", "X-Z"}},
		{name: "partial", order: []string{"x-z"}, want: []string{"X-Z", "X-A", "X-M"}},
		{name: "case insensitive", order: []string{"X-Z", "X-M"}, want: []string{"X-Z", "X-M", "X-A"}},
		{name: "exclude", order: []string{"x-z"}, exclude: map[string]bool{"X-M": true}, want: []string{"X-Z", "X-A"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := Sorted(h, Indices(tc.order), tc.exclude)
			var got []string
			for _, f := range fields {
				got = append(got, f.Key)
				if f.Key == "X-Z" && !reflect.DeepEqual(f.Values, []string{"z1", "z2"}) {
					t.Fatal("values changed")
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOrdersAreIndependent(t *testing.T) {
	h := map[string][]string{"X-A": {"a"}, "X-Z": {"z"}}
	for i := 0; i < 100; i++ {
		fields := Sorted(h, Indices([]string{"x-z", "x-a"}), nil)
		if fields[0].Key != "X-Z" {
			t.Fatal(fields)
		}
		fields = Sorted(h, nil, nil)
		if fields[0].Key != "X-A" {
			t.Fatal("custom order leaked")
		}
	}
}
