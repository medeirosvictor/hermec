package update

import (
	"context"
	"net/http"
	"testing"
)

func TestLatestDistinguishesFailure(t *testing.T) {
	stub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v0.1.0","html_url":"https://github.com/x/y"}`))
	})
	if _, found, err := Latest(context.Background(), "v0.1.0"); found || err != nil {
		t.Fatalf("up to date: found=%v err=%v", found, err)
	}
	if a, found, err := Latest(context.Background(), "v0.0.9"); !found || err != nil || a.Version != "v0.1.0" {
		t.Fatalf("newer: %+v found=%v err=%v", a, found, err)
	}
	stub(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 500) })
	if _, found, err := Latest(context.Background(), "v0.0.9"); found || err == nil {
		t.Fatalf("http 500: found=%v err=%v", found, err)
	}
	stub(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{")) })
	if _, _, err := Latest(context.Background(), "v0.0.9"); err == nil {
		t.Fatal("malformed body should be an error")
	}
	if _, found, err := Latest(context.Background(), "dev"); found || err != nil {
		t.Fatalf("dev: found=%v err=%v", found, err)
	}
}
