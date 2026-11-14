package http2

// HasConnection observes a reusable pooled session without reserving a stream.
// Concurrency limits do not hide a live session; normal request admission remains
// responsible for its queue/dial decision. No connection or stream is created.
func (t *Transport) HasConnection(scheme, authority string) bool {
	addr := authorityAddr(scheme, authority)
	t.connPool.mu.Lock()
	defer t.connPool.mu.Unlock()

	for _, conn := range t.connPool.conns[addr] {
		conn.mu.Lock()
		usable := !conn.singleUse && conn.isUsableLocked()
		conn.mu.Unlock()
		if usable {
			return true
		}
	}

	return false
}
