package inspector

import (
	"strings"
	"unicode/utf8"
)

// StreamWindow is how many bytes of a streamed response are held back before
// being handed to the client. A pattern split across two SSE chunks is only
// catchable if the tail is still in hand, so nothing within a window of the
// stream's leading edge is emitted yet.
const StreamWindow = 256

// maxStreamBuffer bounds the buffer when a match keeps growing (the token
// patterns have no upper length). Past this the pending match is redacted as
// far as it goes rather than buffering the whole response.
const maxStreamBuffer = 32 * 1024

// StreamScanner scans a response while it streams. Feed it the text of each
// chunk; it returns the text that is safe to pass on, holding back anything
// that could still turn out to be the start of a secret.
//
// It is not safe for concurrent use — one scanner belongs to one stream.
type StreamScanner struct {
	ins    *Inspector
	policy Policy

	buf        strings.Builder
	findings   []Finding
	suppressed []Finding
	blocked    bool
}

// NewStreamScanner returns a scanner for one response stream.
func (i *Inspector) NewStreamScanner(p Policy) *StreamScanner {
	return &StreamScanner{ins: i, policy: p}
}

// Feed adds the next piece of streamed text and returns what may be forwarded
// to the client now, redacted where the policy says so. When verdict is
// ActionBlock the caller must stop the stream: the offending text is in the
// held-back window and is never returned.
func (s *StreamScanner) Feed(text string) (emit string, verdict Action) {
	if text != "" {
		s.buf.WriteString(text)
	}
	return s.process(false)
}

// Flush drains the held-back window at the end of a stream.
func (s *StreamScanner) Flush() (emit string, verdict Action) {
	return s.process(true)
}

// Findings are the matches recorded so far, for the incident feed.
func (s *StreamScanner) Findings() []Finding { return s.findings }

// Suppressed are the matches a mute or allowlist entry held back.
func (s *StreamScanner) Suppressed() []Finding { return s.suppressed }

// Verdict is the strictest action seen so far.
func (s *StreamScanner) Verdict() Action { return Verdict(s.findings) }

// process scans the buffer, decides how much of it can leave, and records the
// matches that fall entirely within that part. Matches still growing at the
// leading edge stay buffered so they are seen whole.
func (s *StreamScanner) process(final bool) (string, Action) {
	if s.blocked {
		return "", ActionBlock
	}
	buf := s.buf.String()
	if buf == "" {
		return "", ActionOff
	}

	found, suppressed := s.ins.ScanWithSuppressed(buf, s.policy)

	// One block-action match is enough: the caller tears the stream down and
	// nothing further is written, so the match need not even be complete.
	for _, f := range found {
		if f.Action == ActionBlock {
			s.blocked = true
			s.findings = append(s.findings, complete(f, buf)...)
			s.buf.Reset()
			return "", ActionBlock
		}
	}

	emitLen := len(buf)
	if !final {
		emitLen -= StreamWindow
		if emitLen < 0 {
			emitLen = 0
		}
		// A redaction that is still growing must not be emitted in halves, so
		// pull the boundary back to where it starts. Findings are sorted by
		// position, so walking backwards settles in one pass: every earlier
		// match is then tested against the boundary its successors moved.
		for i := len(found) - 1; i >= 0; i-- {
			if f := found[i]; f.Action == ActionRedact && f.End > emitLen && f.Start < emitLen {
				emitLen = f.Start
			}
		}
		// ...unless it grows without end. The token patterns have no upper
		// length, so a match can outgrow any buffer. Redacting half of it and
		// streaming the rest would leak the tail of a credential, so the
		// stream fails closed instead: the match is recorded as blocked and
		// the caller tears the stream down.
		if emitLen == 0 && len(buf) > maxStreamBuffer {
			s.blocked = true
			s.findings = append(s.findings, blockedByOverflow(found, buf)...)
			s.buf.Reset()
			return "", ActionBlock
		}
		emitLen = alignRune(buf, emitLen)
	}
	if emitLen <= 0 {
		return "", ActionOff
	}

	// Only matches that end inside the emitted part are final; the rest are
	// re-found on the next chunk, once they have stopped growing.
	var settled []Finding
	for _, f := range found {
		if f.End <= emitLen {
			settled = append(settled, f)
		} else if f.Start < emitLen {
			settled = append(settled, clip(f, emitLen, buf))
		}
	}
	for _, f := range suppressed {
		if f.End <= emitLen {
			s.suppressed = append(s.suppressed, f)
		}
	}
	s.findings = append(s.findings, settled...)

	out := Redact(buf[:emitLen], settled)
	rest := buf[emitLen:]
	s.buf.Reset()
	s.buf.WriteString(rest)
	return out, Verdict(settled)
}

// clip trims a match to the part being emitted, so a secret that outgrew the
// buffer is still redacted rather than passed through.
func clip(f Finding, end int, buf string) Finding {
	f.End = end
	f.MaskedSample = MaskSample(buf[f.Start:end])
	return f
}

// complete fills in the masked sample for a match that ends the stream.
func complete(f Finding, buf string) []Finding {
	if f.End > len(buf) {
		f.End = len(buf)
	}
	return []Finding{f}
}

// blockedByOverflow reports the matches that filled the buffer. Their action
// is upgraded to block because that is what happened to the traffic: the
// stream was cut rather than redacted.
func blockedByOverflow(found []Finding, buf string) []Finding {
	var out []Finding
	for _, f := range found {
		if f.Action != ActionRedact || f.End <= len(buf)-StreamWindow {
			continue
		}
		f.Action = ActionBlock
		out = append(out, clip(f, len(buf), buf))
	}
	return out
}

// alignRune pulls an offset back to a rune boundary so a multi-byte character
// is never cut in half mid-stream.
func alignRune(s string, i int) int {
	for i > 0 && i < len(s) && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}
