//go:build !nethttpomithttp2

package http

func (t *Transport) hasHTTP2Connection(cm connectMethod) bool {
	t.idleMu.Lock()
	defer t.idleMu.Unlock()

	for _, conn := range t.idleConn[cm.key()] {
		pooled, ok := conn.alt.(http2RoundTripper)
		if ok && !conn.isBroken() &&
			pooled.t.HasConnection(cm.targetScheme, cm.targetAddr) {
			return true
		}
	}

	return false
}
