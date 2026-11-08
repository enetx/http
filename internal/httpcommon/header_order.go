package httpcommon

import (
	"maps"
	"strconv"
	"strings"

	"github.com/enetx/http/internal/headerorder"
)

const (
	HeaderOrderKey  = "Header-Order:"
	PHeaderOrderKey = "PHeader-Order:"
)

func orderedRequestHeaders(req Request) []headerorder.Field {
	order := headerorder.Indices(req.Header[HeaderOrderKey])
	headers := maps.Clone(req.Header)
	for k := range headers {
		if strings.EqualFold(k, "content-length") {
			delete(headers, k)
		}
	}
	if _, ok := order["content-length"]; ok && shouldSendReqContentLength(req.Method, req.ActualContentLength) {
		headers["content-length"] = []string{strconv.FormatInt(req.ActualContentLength, 10)}
	}
	return headerorder.Sorted(headers, order, nil)
}

// Return false to retain the upstream pseudo-header defaults when no order is supplied.
func writeOrderedPseudoHeaders(req Request, host, path string, normalConnect bool, protocol string, f func(string, string)) bool {
	order, ok := req.Header[PHeaderOrderKey]
	if !ok {
		return false
	}
	method := req.Method
	if method == "" {
		method = "GET"
	}
	for _, name := range order {
		switch name {
		case ":authority":
			f(name, host)
		case ":method":
			f(name, method)
		case ":path":
			if !normalConnect {
				f(name, path)
			}
		case ":scheme":
			if !normalConnect {
				f(name, req.URL.Scheme)
			}
		}
	}
	// The original fork places Extended CONNECT's :protocol after the ordered fields.
	if protocol != "" {
		f(":protocol", protocol)
	}
	return true
}
