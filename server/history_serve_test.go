package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/proto"
)

var hexID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// readHistory reads the next history frame, skipping roster frames; any other
// frame type fails the test.
func readHistory(t *testing.T, c *websocket.Conn) proto.History {
	t.Helper()
	env := readEnvSkipRoster(t, c)
	if env.Type != proto.TypeHistory {
		t.Fatalf("type = %q (%s), want history", env.Type, env.Data)
	}
	var h proto.History
	if err := json.Unmarshal(env.Data, &h); err != nil {
		t.Fatal(err)
	}
	return h
}

// joinGeneral joins general and consumes the presence frame.
func joinGeneral(t *testing.T, c *websocket.Conn) {
	t.Helper()
	sendEnv(t, c, proto.TypeJoin, proto.Join{Channel: "general"})
	readPresence(t, c)
}

func readChat(t *testing.T, c *websocket.Conn) proto.ChatMessage {
	t.Helper()
	env := readEnvSkipRoster(t, c)
	if env.Type != proto.TypeChatMessage {
		t.Fatalf("type = %q (%s), want chat_message", env.Type, env.Data)
	}
	var m proto.ChatMessage
	if err := json.Unmarshal(env.Data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func seedHistory(t *testing.T, s *Server, n int) []proto.ChatMessage {
	t.Helper()
	base := time.Now().UTC().Add(-time.Hour)
	var all []proto.ChatMessage
	for i := 0; i < n; i++ {
		m := proto.ChatMessage{
			ID: fmt.Sprintf("%032x", i+1), Channel: "general",
			Text: fmt.Sprintf("m%d", i), TS: base.Add(time.Duration(i) * time.Second),
		}
		if err := s.hist.Add(m); err != nil {
			t.Fatal(err)
		}
		all = append(all, m)
	}
	return all
}

func TestJoinSendsHistory(t *testing.T) {
	url := startServer(t, chatCfg())
	a, _ := authed(t, url)
	joinGeneral(t, a)
	for i := 0; i < 3; i++ {
		sendEnv(t, a, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: fmt.Sprintf("t%d", i)})
		readChat(t, a)
	}
	b, _ := authed(t, url)
	sendEnv(t, b, proto.TypeJoin, proto.Join{Channel: "general"})
	readPresence(t, b)
	h := readHistory(t, b)
	if h.Channel != "general" || h.Before != "" || h.More || len(h.Messages) != 3 {
		t.Fatalf("history = %+v", h)
	}
	for i, m := range h.Messages {
		if m.Text != fmt.Sprintf("t%d", i) || !hexID.MatchString(m.ID) {
			t.Fatalf("message %d = %+v", i, m)
		}
	}
}

func TestJoinNoHistoryWhenEmpty(t *testing.T) {
	url := startServer(t, chatCfg())
	a, _ := authed(t, url)
	joinGeneral(t, a)
	requireNothing(t, a)
}

func TestHistorySurvivesRestart(t *testing.T) {
	cfg := chatCfg()
	cfg.HostKey = mustID(t)
	ch, err := newChain(cfg.HostKey, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Chain = ch
	cfg.HistoryPath = filepath.Join(t.TempDir(), "h.jsonl")

	s1, url := startServerS(t, cfg)
	a, _ := authed(t, url)
	joinGeneral(t, a)
	for _, txt := range []string{"one", "two"} {
		sendEnv(t, a, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: txt})
		readChat(t, a)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s1.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	_, url2 := startServerS(t, cfg)
	b, _ := authed(t, url2)
	sendEnv(t, b, proto.TypeJoin, proto.Join{Channel: "general"})
	readPresence(t, b)
	h := readHistory(t, b)
	if len(h.Messages) != 2 || h.Messages[0].Text != "one" || h.Messages[1].Text != "two" {
		t.Fatalf("history = %+v", h)
	}
}

func TestHistoryBeforePages(t *testing.T) {
	s, url := startServerS(t, chatCfg())
	all := seedHistory(t, s, 150)
	b, _ := authed(t, url)
	sendEnv(t, b, proto.TypeJoin, proto.Join{Channel: "general"})
	readPresence(t, b)
	h := readHistory(t, b)
	if len(h.Messages) != 100 || !h.More || h.Messages[0].ID != all[50].ID || h.Messages[99].ID != all[149].ID {
		t.Fatalf("join page: %d msgs, more=%v", len(h.Messages), h.More)
	}
	oldest := h.Messages[0].ID
	sendEnv(t, b, proto.TypeHistoryBefore, proto.HistoryBefore{Channel: "general", BeforeID: oldest})
	h2 := readHistory(t, b)
	if len(h2.Messages) != 50 || h2.More || h2.Before != oldest || h2.Messages[0].ID != all[0].ID {
		t.Fatalf("older page: %d msgs, more=%v before=%q", len(h2.Messages), h2.More, h2.Before)
	}
}

func TestHistoryBeforeNotJoined(t *testing.T) {
	s, url := startServerS(t, chatCfg())
	all := seedHistory(t, s, 3)
	a, _ := authed(t, url)
	sendEnv(t, a, proto.TypeHistoryBefore, proto.HistoryBefore{Channel: "general", BeforeID: all[2].ID})
	requireErrCode(t, a, "not_joined")
	requireNothing(t, a)
}

func TestHistoryBeforeMalformed(t *testing.T) {
	url := startServer(t, chatCfg())
	a, _ := authed(t, url)
	sendEnv(t, a, proto.TypeHistoryBefore, "junk")
	requireErrCode(t, a, "bad_request")
}

func TestHistoryBeforeUnknownID(t *testing.T) {
	s, url := startServerS(t, chatCfg())
	seedHistory(t, s, 3)
	a, _ := authed(t, url)
	joinGeneral(t, a)
	readHistory(t, a)
	sendEnv(t, a, proto.TypeHistoryBefore, proto.HistoryBefore{Channel: "general", BeforeID: "nope"})
	h := readHistory(t, a)
	if h.Messages == nil || len(h.Messages) != 0 || h.More || h.Before != "nope" {
		t.Fatalf("history = %+v", h)
	}
}

func TestHistoryFrameFitsLimit(t *testing.T) {
	hs, err := OpenHistory("")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	text := strings.Repeat("\x01", 4096)
	for i := 0; i < 20; i++ {
		m := proto.ChatMessage{ID: fmt.Sprintf("%032x", i+1), Channel: "general", Text: text, TS: base.Add(time.Duration(i) * time.Second)}
		if err := hs.Add(m); err != nil {
			t.Fatal(err)
		}
	}
	msgs, more := hs.Page("general", "", historyPage)
	if len(msgs) != 20 || more {
		t.Fatalf("page = %d, %v", len(msgs), more)
	}
	raw := encodeHistory(proto.History{Channel: "general", Messages: msgs})
	if len(raw) == 0 || len(raw) > proto.MaxMessageSize {
		t.Fatalf("frame is %d bytes (max %d)", len(raw), proto.MaxMessageSize)
	}
	env, err := proto.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	var h proto.History
	if err := json.Unmarshal(env.Data, &h); err != nil {
		t.Fatal(err)
	}
	if !h.More || len(h.Messages) < 1 || len(h.Messages) >= 20 {
		t.Fatalf("kept %d, more=%v", len(h.Messages), h.More)
	}
	if h.Messages[len(h.Messages)-1].ID != msgs[19].ID {
		t.Error("newest message was dropped; must drop from the oldest end")
	}
}

func TestChatNotStoredWhenDaysZero(t *testing.T) {
	cfg := chatCfg()
	zero := 0
	cfg.HistoryDays = &zero
	url := startServer(t, cfg)
	a, _ := authed(t, url)
	joinGeneral(t, a)
	sendEnv(t, a, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "x"})
	if m := readChat(t, a); !hexID.MatchString(m.ID) {
		t.Fatalf("live message id = %q", m.ID)
	}
	b, _ := authed(t, url)
	joinGeneral(t, b)
	requireNothing(t, b)
}

func TestConfigAppendAppliesHistoryLimits(t *testing.T) {
	founder := mustID(t)
	cfg := chatCfg()
	cfg.HostKey = founder
	ch, err := newChain(founder, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Chain = ch
	cfg.HistoryPath = filepath.Join(t.TempDir(), "h.jsonl")
	f := &appendFixture{url: startServer(t, cfg), founder: founder, ch: ch}
	a := f.connect(t, f.founder)
	joinGeneral(t, a)
	sendEnv(t, a, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "before"})
	readChat(t, a)

	body := f.ch.State().Config
	zero := 0
	body.HistoryDays = &zero
	sendAppend(t, a, f.entry(t, f.founder, chain.OpConfig, body))
	readChainEntry(t, a)

	sendEnv(t, a, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "after"})
	if m := readChat(t, a); m.Text != "after" {
		t.Fatalf("got %+v", m)
	}
	b := f.connect(t, mustID(t))
	sendEnv(t, b, proto.TypeJoin, proto.Join{Channel: "general"})
	readPresence(t, b)
	// The append trimmed at once: nothing is served and the file is gone.
	requireNothing(t, b)
	if _, err := os.Stat(cfg.HistoryPath); !os.IsNotExist(err) {
		t.Fatalf("history file still exists (err=%v)", err)
	}
}
