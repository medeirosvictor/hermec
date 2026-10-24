package ui

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestChatWriteFailureShowsGenericBannerAndLogsDetails(t *testing.T) {
	g := newTestGame(t)
	s := newSession("server-key", "ws://127.0.0.1:17697/", "me")
	g.sess = map[string]*session{s.key: s}
	g.curKey = s.key
	var logs bytes.Buffer
	g.setLogWriter(&logs)

	const text = "private message contents"
	id := s.queueChat(nil, "general", text)
	g.sendCh <- sendResult{key: s.key, chatID: id, err: errors.New("broken pipe")}
	g.pumpSessions()

	if s.st.Status != "message unable to reach server" {
		t.Fatalf("status = %q, want generic send failure", s.st.Status)
	}
	if len(s.outgoing) != 0 {
		t.Fatalf("failed chat remained pending: %+v", s.outgoing)
	}
	got := logs.String()
	for _, want := range []string{s.url, "general", "broken pipe"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log = %q, want %q", got, want)
		}
	}
	if strings.Contains(got, text) {
		t.Fatalf("log contains private message text: %q", got)
	}
}
