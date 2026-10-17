package discover

import (
	"context"
	"errors"
	"testing"
)

func TestSelfTailscaleIP(t *testing.T) {
	oldLook, oldRun := lookPath, runStatus
	t.Cleanup(func() { lookPath, runStatus = oldLook, oldRun })

	lookPath = func(string) (string, error) { return "tailscale", nil }
	runStatus = func(context.Context, string) ([]byte, error) {
		return []byte(`{"Self":{"TailscaleIPs":["fd7a::1","100.101.102.103"]}}`), nil
	}
	if got := selfTailscaleIP(context.Background()); got != "100.101.102.103" {
		t.Errorf("got %q", got)
	}

	lookPath = func(string) (string, error) { return "", errors.New("missing") }
	if got := selfTailscaleIP(context.Background()); got != "" {
		t.Errorf("got %q with no CLI", got)
	}
}
