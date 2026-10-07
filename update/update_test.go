package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		remote, local string
		want          bool
	}{
		{"v0.10.0", "v0.9.9", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.1.1", "v0.1.0", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"v0.9.9", "v0.10.0", false},
		{"0.2.0", "v0.1.0", false},
		{"v0.2.0", "0.1.0", false},
		{"v0.2", "v0.1.0", false},
		{"v0.2.0-rc1", "v0.1.0", false},
		{"v0.2.x", "v0.1.0", false},
		{"v0.2.0.1", "v0.1.0", false},
		{"v-1.0.0", "v0.1.0", false},
		{"", "v0.1.0", false},
		{"v0.2.0", "", false},
		{"v0.2.0", "dev", false},
	}
	for _, c := range cases {
		if got := newer(c.remote, c.local); got != c.want {
			t.Errorf("newer(%q,%q)=%v want %v", c.remote, c.local, got, c.want)
		}
	}
}

func stub(t *testing.T, h http.HandlerFunc) *int32 {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		h(w, r)
	}))
	old := endpoint
	endpoint = srv.URL
	t.Cleanup(func() { endpoint = old; srv.Close() })
	return &hits
}

func TestCheck(t *testing.T) {
	ok := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }
	}
	cases := []struct {
		name    string
		h       http.HandlerFunc
		current string
		want    Available
		wantOK  bool
	}{
		{"happy", ok(`{"tag_name":"v0.2.0","html_url":"https://x/r","name":"ignored"}`), "v0.1.0", Available{"v0.2.0", "https://x/r"}, true},
		{"same", ok(`{"tag_name":"v0.1.0","html_url":"u"}`), "v0.1.0", Available{}, false},
		{"older", ok(`{"tag_name":"v0.0.9","html_url":"u"}`), "v0.1.0", Available{}, false},
		{"404", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }, "v0.1.0", Available{}, false},
		{"404 with valid body", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(404)
			w.Write([]byte(`{"tag_name":"v9.0.0","html_url":"u"}`))
		}, "v0.1.0", Available{}, false},
		{"garbage", ok(`<<not json>>`), "v0.1.0", Available{}, false},
		{"malformed tag", ok(`{"tag_name":"nightly","html_url":"u"}`), "v0.1.0", Available{}, false},
		{"prerelease tag rejected", ok(`{"tag_name":"v0.2.0-rc1","html_url":"u"}`), "v0.1.0", Available{}, false},
		{"malformed current", ok(`{"tag_name":"v0.2.0","html_url":"u"}`), "garbage", Available{}, false},
		{"oversize body capped", ok(`{"tag_name":"v0.2.0","html_url":"u","pad":"` + strings.Repeat("a", 70<<10) + `"}`), "v0.1.0", Available{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stub(t, c.h)
			got, ok := Check(context.Background(), c.current)
			if ok != c.wantOK || got != c.want {
				t.Fatalf("got %+v %v want %+v %v", got, ok, c.want, c.wantOK)
			}
		})
	}
}

func TestCheckDevMakesNoRequest(t *testing.T) {
	hits := stub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v9.9.9","html_url":"u"}`))
	})
	if _, ok := Check(context.Background(), "dev"); ok {
		t.Fatal("dev must not report an update")
	}
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Fatalf("dev made %d requests", n)
	}
}

func TestCheckTimeoutViaContext(t *testing.T) {
	release := make(chan struct{})
	stub(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, ok := Check(ctx, "v0.1.0"); ok {
		t.Fatal("want false on timeout")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("did not honor ctx: %v", time.Since(start))
	}
}

func TestCheckUnreachable(t *testing.T) {
	old := endpoint
	defer func() { endpoint = old }()
	endpoint = "http://127.0.0.1:1/"
	if _, ok := Check(context.Background(), "v0.1.0"); ok {
		t.Fatal("want false")
	}
}
