package worker

import "bytes"

// boundedWriter is an io.Writer that keeps at most limit bytes and silently
// discards the rest. It never fails and never grows past limit, so a
// submission printing gigabytes can't exhaust worker memory
type boundedWriter struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newBoundedWriter(limit int) *boundedWriter {
	return &boundedWriter{limit: limit}
}

func (b *boundedWriter) Write(p []byte) (int, error) {
	remaining := b.limit - b.buf.Len()
	switch {
	case remaining <= 0:
		b.truncated = true
	case len(p) > remaining:
		b.buf.Write(p[:remaining])
		b.truncated = true
	default:
		b.buf.Write(p)
	}
	// Report all bytes consumed so the child never blocks on a full pipe.
	return len(p), nil
}

func (b *boundedWriter) String() string {
	return b.buf.String()
}

// Truncated reports whether any bytes were discarded.
func (b *boundedWriter) Truncated() bool {
	return b.truncated
}
