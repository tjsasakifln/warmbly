package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestCohortReviewUsageMentionsIDActorConfirm(t *testing.T) {
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	code := cmdCohortReview(nil)
	_ = w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()
	if code != 2 {
		t.Fatalf("missing flags must exit 2, got %d", code)
	}
	out := buf.String()
	for _, want := range []string{"--id", "--actor", "--confirm"} {
		if !strings.Contains(out, want) {
			t.Fatalf("usage missing %s: %s", want, out)
		}
	}
}
