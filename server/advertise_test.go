package server

import "testing"

func TestPasswordKeyHookFiresOnChange(t *testing.T) {
	s := New(Config{})
	var got [][]byte
	s.SetPasswordKeyHook(func(k []byte) { got = append(got, k) })
	a, b := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	s.setPasswordKey(a)
	s.setPasswordKey(a)
	s.setPasswordKey(b)
	if len(got) != 2 || string(got[0]) != string(a) || string(got[1]) != string(b) {
		t.Fatalf("hook calls = %q", got)
	}
}

func TestSetAdvertise(t *testing.T) {
	s := New(Config{Advertise: []string{"ws://x/"}})
	if got := s.advertise(); len(got) != 1 || got[0] != "ws://x/" {
		t.Fatalf("advertise = %v", got)
	}
	s.SetAdvertise([]string{"ws://y/"})
	if got := s.advertise(); len(got) != 1 || got[0] != "ws://y/" {
		t.Fatalf("advertise = %v", got)
	}
}
