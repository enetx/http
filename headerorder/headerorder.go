// Package headerorder exposes the common wire-header sorter to protocol adapters
// maintained outside the HTTP module. All protocols share one implementation.
package headerorder

import "github.com/enetx/http/internal/headerorder"

const (
	HeaderOrderKey  = headerorder.HeaderOrderKey
	PHeaderOrderKey = headerorder.PHeaderOrderKey
)

type Field = headerorder.Field

func Indices(names []string) map[string]int { return headerorder.Indices(names) }

// Sorted returns independent ordering without modifying headers or exclusions.
func Sorted(h map[string][]string, order map[string]int, exclude map[string]bool) []Field {
	return headerorder.Sorted(h, order, exclude)
}
