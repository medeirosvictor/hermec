package ui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFitCallTitle(t *testing.T) {
	if got := fitCallTitle("voice", "01:23", 40); got != "voice  01:23" {
		t.Errorf("short = %q", got)
	}
	long := "voice @ " + strings.Repeat("n", 64)
	got := fitCallTitle(long, "01:23", 30)
	if utf8.RuneCountInString(got) > 30 || !strings.HasSuffix(got, "  01:23") {
		t.Errorf("long = %q (%d runes)", got, utf8.RuneCountInString(got))
	}
	if got := fitCallTitle(long, "01:23", 5); got != "01:23" {
		t.Errorf("tiny = %q, want time only", got)
	}
}
