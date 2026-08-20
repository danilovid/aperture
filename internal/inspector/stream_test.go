package inspector

import (
	"strings"
	"testing"
)

// feedAll streams text in fixed-size chunks, the way SSE deltas arrive.
func feedAll(t *testing.T, s *StreamScanner, text string, chunk int) (out string, verdict Action) {
	t.Helper()
	verdict = ActionOff
	var b strings.Builder
	for i := 0; i < len(text); i += chunk {
		end := min(i+chunk, len(text))
		emit, v := s.Feed(text[i:end])
		b.WriteString(emit)
		if v == ActionBlock {
			return b.String(), ActionBlock
		}
	}
	emit, v := s.Flush()
	b.WriteString(emit)
	if v.severity() > verdict.severity() {
		verdict = v
	}
	return b.String(), verdict
}

func redactPolicy() Policy {
	return Policy{Secrets: ActionRedact, PII: ActionRedact, Custom: ActionOff}
}

// The whole point of the window: a secret split across chunks is still caught.
func TestStreamCatchesSecretSplitAcrossChunks(t *testing.T) {
	ins := New()
	text := "here is the key AKIAIOSFODNN7EXAMPLE, use it well"

	for _, chunk := range []int{1, 3, 7, 20, 1000} {
		s := ins.NewStreamScanner(redactPolicy())
		out, _ := feedAll(t, s, text, chunk)
		if strings.Contains(out, "AKIAIOSFODNN7EXAMPLE") {
			t.Errorf("chunk=%d: secret reached the client: %q", chunk, out)
		}
		if !strings.Contains(out, "[REDACTED:aws-access-key]") {
			t.Errorf("chunk=%d: not redacted: %q", chunk, out)
		}
		if want := "here is the key [REDACTED:aws-access-key], use it well"; out != want {
			t.Errorf("chunk=%d: got %q, want %q", chunk, out, want)
		}
		if len(s.Findings()) != 1 {
			t.Errorf("chunk=%d: findings = %+v, want exactly one", chunk, s.Findings())
		}
	}
}

// Clean text must stream through unchanged, minus the trailing window that
// Flush releases at the end.
func TestStreamPassesCleanTextThrough(t *testing.T) {
	ins := New()
	text := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 40)
	s := ins.NewStreamScanner(redactPolicy())
	out, verdict := feedAll(t, s, text, 13)
	if out != text {
		t.Errorf("clean text was altered:\n got %q\nwant %q", out, text)
	}
	if verdict != ActionOff {
		t.Errorf("verdict = %q, want off", verdict)
	}
	if len(s.Findings()) != 0 {
		t.Errorf("findings on clean text: %+v", s.Findings())
	}
}

// Multi-byte characters must not be cut in half at a window boundary.
func TestStreamDoesNotSplitRunes(t *testing.T) {
	ins := New()
	text := strings.Repeat("привет мир, как дела? ", 30)
	s := ins.NewStreamScanner(redactPolicy())
	out, _ := feedAll(t, s, text, 5)
	if out != text {
		t.Errorf("utf-8 text was mangled:\n got %q\nwant %q", out, text)
	}
	if strings.Contains(out, "�") {
		t.Error("emitted a replacement character — a rune was split")
	}
}

// Nothing of a blocked secret may reach the client, and the caller learns to
// tear the stream down.
func TestStreamBlocksBeforeEmitting(t *testing.T) {
	ins := New()
	s := ins.NewStreamScanner(Policy{Secrets: ActionBlock, PII: ActionOff, Custom: ActionOff})
	out, verdict := feedAll(t, s, "sure, the token is ghp_"+strings.Repeat("a", 36)+" — enjoy", 4)
	if verdict != ActionBlock {
		t.Fatalf("verdict = %q, want block", verdict)
	}
	if strings.Contains(out, "ghp_") {
		t.Errorf("blocked secret leaked into the stream: %q", out)
	}
	if len(s.Findings()) == 0 || s.Findings()[0].Rule != "github-token" {
		t.Errorf("findings = %+v, want the github-token match", s.Findings())
	}
	// Once blocked, the scanner stays blocked.
	if emit, v := s.Feed("more text"); emit != "" || v != ActionBlock {
		t.Errorf("scanner resumed after a block: %q %q", emit, v)
	}
}

// The window is a promise about how much is held back, not how much is kept
// forever: text older than the window must not be stuck in the buffer.
func TestStreamEmitsBeyondTheWindow(t *testing.T) {
	ins := New()
	s := ins.NewStreamScanner(redactPolicy())
	emit, _ := s.Feed(strings.Repeat("x", StreamWindow*3))
	if len(emit) != StreamWindow*2 {
		t.Errorf("emitted %d bytes, want %d (everything but the window)", len(emit), StreamWindow*2)
	}
	rest, _ := s.Flush()
	if len(rest) != StreamWindow {
		t.Errorf("flush released %d bytes, want %d", len(rest), StreamWindow)
	}
}

// A match with no end in sight cannot be redacted safely mid-stream: emitting
// the first half and streaming the rest would hand over the tail of a
// credential. The stream fails closed instead.
func TestStreamFailsClosedOnEndlessMatch(t *testing.T) {
	ins := New()
	s := ins.NewStreamScanner(redactPolicy())
	// A JWT-shaped blob far longer than the buffer cap.
	jwt := "eyJ" + strings.Repeat("a", 20) + "." + strings.Repeat("b", 20) + "." + strings.Repeat("c", maxStreamBuffer*2)
	out, verdict := feedAll(t, s, jwt, 4096)
	if verdict != ActionBlock {
		t.Fatalf("verdict = %q, want block", verdict)
	}
	if strings.Contains(out, "eyJ") || strings.Contains(out, strings.Repeat("c", 100)) {
		t.Errorf("the endless match streamed through: %.120q", out)
	}
	if len(s.Findings()) == 0 || s.Findings()[0].Rule != "jwt" {
		t.Errorf("findings = %+v, want the jwt match recorded", s.Findings())
	}
	if s.Findings()[0].Action != ActionBlock {
		t.Errorf("action = %q, want it recorded as blocked — the stream was cut, not redacted",
			s.Findings()[0].Action)
	}
}

// Muted matches are recorded but not redacted, exactly as in the request path.
func TestStreamRecordsSuppressed(t *testing.T) {
	ins := New()
	p := redactPolicy()
	p.MutedRules = []string{"email"}
	s := ins.NewStreamScanner(p)
	out, verdict := feedAll(t, s, "write to alice@example.com please", 6)
	if !strings.Contains(out, "alice@example.com") {
		t.Errorf("muted match was redacted anyway: %q", out)
	}
	if verdict != ActionOff {
		t.Errorf("verdict = %q, want off for a muted match", verdict)
	}
	if len(s.Suppressed()) != 1 {
		t.Errorf("suppressed = %+v, want one", s.Suppressed())
	}
}
