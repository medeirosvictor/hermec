package client

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/medeirosvictor/hermec/core/identity"
)

// A server that refuses before the WebSocket upgrade answers with a plain
// HTTP status; Dial must surface it as a typed ServerError, not a bare
// "bad handshake".
func TestDialRefusedBeforeUpgrade(t *testing.T) {
	cases := []struct {
		status int
		code   string
	}{
		{http.StatusServiceUnavailable, CodeServerBusy},
		{http.StatusForbidden, CodeForbidden},
	}
	for _, tc := range cases {
		status := tc.status
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "refused", status)
		}))
		t.Cleanup(srv.Close)
		url := "ws://" + strings.TrimPrefix(srv.URL, "http://") + "/"
		id, err := identity.Generate()
		if err != nil {
			t.Fatal(err)
		}
		_, err = Dial(testCtx(t), url, id, "alice", "")
		var se *ServerError
		if !errors.As(err, &se) {
			t.Fatalf("status %d: err = %v (%T), want *ServerError", status, err, err)
		}
		if se.Code != tc.code {
			t.Errorf("status %d: Code = %q, want %q", status, se.Code, tc.code)
		}
		if se.Message == "" {
			t.Errorf("status %d: empty Message", status)
		}
	}
}

// Other handshake failures keep their original error text.
func TestDialOtherHandshakeErrorUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	url := "ws://" + strings.TrimPrefix(srv.URL, "http://") + "/"
	id, _ := identity.Generate()
	_, err := Dial(testCtx(t), url, id, "alice", "")
	var se *ServerError
	if err == nil || errors.As(err, &se) {
		t.Fatalf("err = %v, want a plain dial error", err)
	}
	if !strings.Contains(err.Error(), "client: dial:") {
		t.Errorf("err = %q, want the client: dial: prefix", err)
	}
}
