package http

import (
	"strconv"

	"github.com/enetx/http/internal/headerorder"
)

// HeaderOrderKey is a magic key for ResponseWriter.Header map keys
// that, if present, defines a header order that will be used to
// write the headers onto wire. The order of the list defined how the headers
// will be sorted. A defined key goes before an undefined key.
//
// This is the only way to specify some order, because maps don't
// have a a stable iteration order. If no order is given, headers will
// be sorted lexicographically.
//
// According to RFC-2616 it is good practice to send general-header fields
// first, followed by request-header or response-header fields and ending
// with entity-header fields.
const HeaderOrderKey = "Header-Order:"

// PHeaderOrderKey is a magic key for setting http2 pseudo header order.
// If the header is nil it will use regular GoLang header order.
// Valid fields are :authority, :method, :path, :scheme
const PHeaderOrderKey = "PHeader-Order:"

// Only explicit ordered Content-Length requires a private copy. The shared
// sorter filters metadata and leaves the caller's exclusion map untouched.
func orderedHeaders(h Header, exclude map[string]bool, cl int64) []headerorder.Field {
	order := headerorder.Indices(h[HeaderOrderKey])
	if _, ok := order["content-length"]; ok && cl >= 0 {
		h = h.Clone()
		h["content-length"] = []string{strconv.FormatInt(cl, 10)}
	}
	return headerorder.Sorted(h, order, exclude)
}
