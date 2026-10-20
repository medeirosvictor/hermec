package main

import (
	"errors"
	"strings"
	"testing"
)

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("invalid handle") }

func TestFileFirstWriterSurvivesBadConsole(t *testing.T) {
	var f strings.Builder
	w := fileFirstWriter{file: &f, console: failWriter{}}
	n, err := w.Write([]byte("line\n"))
	if err != nil || n != 5 || f.String() != "line\n" {
		t.Fatalf("n=%d err=%v file=%q", n, err, f.String())
	}
}

func TestFileFirstWriterWritesBoth(t *testing.T) {
	var f, c strings.Builder
	_, _ = fileFirstWriter{file: &f, console: &c}.Write([]byte("x"))
	if f.String() != "x" || c.String() != "x" {
		t.Fatal("both sinks must get the line")
	}
}
