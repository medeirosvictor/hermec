package state

import (
	"testing"
	"time"
)

func TestGate(t *testing.T) {
	t0 := time.Unix(100, 0)
	var g Gate
	step := func(in GateInput, at time.Duration) bool { return g.Step(in, t0.Add(at)) }

	if !step(GateInput{}, 0) {
		t.Error("open mic transmits")
	}
	if step(GateInput{Muted: true}, 0) {
		t.Error("open mic muted must not transmit")
	}
	ptt := GateInput{PTT: true}
	if step(ptt, 0) {
		t.Error("ptt idle must not transmit")
	}
	held := ptt
	held.Held = true
	if !step(held, time.Second) {
		t.Error("ptt held transmits")
	}
	if !step(ptt, time.Second+PTTHangover/2) {
		t.Error("hangover keeps the gate open")
	}
	if step(ptt, time.Second+PTTHangover+time.Millisecond) {
		t.Error("gate closes after hangover")
	}
	held.Muted = true
	if step(held, 2*time.Second) {
		t.Error("mute overrides held key")
	}
	if step(ptt, 2*time.Second+time.Millisecond) {
		t.Error("mute clears the hangover")
	}
}

func TestGateTextFocus(t *testing.T) {
	var g Gate
	now := time.Unix(100, 0)
	in := GateInput{PTT: true, Held: true, WindowFocused: true, TextFocus: true, KeyTypes: true}
	if g.Step(in, now) {
		t.Error("typing key in a focused text field must not transmit")
	}
	in.KeyTypes = false
	if !g.Step(in, now) {
		t.Error("non-typing key transmits while typing")
	}
	g = Gate{}
	in.KeyTypes, in.WindowFocused = true, false
	if !g.Step(in, now) {
		t.Error("unfocused window has no typing: global key transmits")
	}
}

func TestPTTSettings(t *testing.T) {
	var s Settings
	if s.PushToTalk() || s.VoiceModeLabel() != "open mic" || s.PTTKeyChoice().ID != "rctrl" {
		t.Error("defaults")
	}
	s.ToggleVoiceMode()
	if !s.PushToTalk() || s.VoiceMode != VoicePTT {
		t.Error("toggle on")
	}
	s.ToggleVoiceMode()
	if s.VoiceMode != "" {
		t.Error("open mic stored as empty")
	}
	seen := map[string]bool{}
	for i := 0; i < len(PTTKeys); i++ {
		s.CyclePTTKey()
		seen[s.PTTKeyChoice().ID] = true
	}
	if len(seen) != len(PTTKeys) || s.PTTKey != "" {
		t.Errorf("cycle seen=%v key=%q", seen, s.PTTKey)
	}
	s.PTTKey = "bogus"
	if s.PTTKeyChoice().ID != "rctrl" || NextPTTKey("bogus") != "grave" {
		t.Error("unknown key falls back to default")
	}
}

func TestGateHeld(t *testing.T) {
	var g Gate
	now := time.Unix(100, 0)
	in := GateInput{PTT: true, Held: true}
	g.Step(in, now)
	if !g.Held() {
		t.Error("held key not reported")
	}
	in.Held = false
	g.Step(in, now.Add(time.Millisecond))
	if g.Held() || !g.Open() {
		t.Errorf("hangover: Held=%v Open=%v, want false/true", g.Held(), g.Open())
	}
	in.Held, in.Muted = true, true
	g.Step(in, now)
	if g.Held() {
		t.Error("muted key counted as held")
	}
}
