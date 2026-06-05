// Package logbuf is an in-memory ring buffer of recent log lines. The
// coordinator tees its log output into a Hub (via log.SetOutput) so the admin
// dashboard can poll recent lines without a streaming connection or a broker.
package logbuf

import (
	"bytes"
	"sync"
	"time"
)

// Entry is one captured log line with a monotonic sequence number.
type Entry struct {
	Seq  int64     `json:"seq"`
	Time time.Time `json:"time"`
	Msg  string    `json:"msg"`
}

// Hub is a bounded, concurrency-safe ring buffer of log lines. It implements
// io.Writer so it can be wired into the standard logger.
type Hub struct {
	mu      sync.Mutex
	buf     []Entry
	cap     int
	seq     int64
	partial []byte // carries an incomplete trailing line between writes
}

// New returns a Hub keeping the most recent capacity lines (default 500).
func New(capacity int) *Hub {
	if capacity <= 0 {
		capacity = 500
	}
	return &Hub{cap: capacity}
}

// Write implements io.Writer: it splits the input on newlines and stores each
// completed line. A partial trailing line is buffered until the next write.
func (h *Hub) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.partial = append(h.partial, p...)
	for {
		i := bytes.IndexByte(h.partial, '\n')
		if i < 0 {
			break
		}
		line := string(h.partial[:i])
		h.partial = append(h.partial[:0], h.partial[i+1:]...)
		h.seq++
		h.buf = append(h.buf, Entry{Seq: h.seq, Time: time.Now(), Msg: line})
		if len(h.buf) > h.cap {
			h.buf = h.buf[len(h.buf)-h.cap:]
		}
	}
	return len(p), nil
}

// Since returns the entries with Seq greater than since (capped to the buffer),
// and the latest sequence number so the caller can poll incrementally.
func (h *Hub) Since(since int64) ([]Entry, int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Entry, 0, len(h.buf))
	for _, e := range h.buf {
		if e.Seq > since {
			out = append(out, e)
		}
	}
	return out, h.seq
}
