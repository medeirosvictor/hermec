package main

import "testing"

func TestDedicatedPrepareAndRetryConflict(t *testing.T) {
	if _, err := parseFlags([]string{"-dedicated-prepare", "wss://host/", "-dedicated-retry"}); err == nil {
		t.Fatal("accepted prepare and retry together")
	}
}
