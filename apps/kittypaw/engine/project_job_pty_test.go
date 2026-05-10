package engine

import (
	"strings"
	"testing"
)

func TestSanitizeProjectPTYTranscriptStripsANSIAndOSC(t *testing.T) {
	raw := "\x1b[31mred\x1b[0m\n\x1b]0;title\x07prompt\tok\r\n"
	got := sanitizeProjectPTYTranscript([]byte(raw))
	if strings.Contains(got, "\x1b") || strings.Contains(got, "]0;title") {
		t.Fatalf("sanitized transcript still has control sequence: %q", got)
	}
	if got != "red\nprompt\tok\r\n" {
		t.Fatalf("sanitized transcript = %q", got)
	}
}

func TestSanitizeProjectPTYTranscriptReplacesInvalidUTF8(t *testing.T) {
	got := sanitizeProjectPTYTranscript([]byte{'o', 'k', 0xff, '\n'})
	if got != "ok�\n" {
		t.Fatalf("sanitized invalid utf8 = %q", got)
	}
}
