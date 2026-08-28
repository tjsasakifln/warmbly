package main

import (
	"os"
	"strings"
	"testing"
)

func TestCLIWiresCohortStoreForReplyIngest(t *testing.T) {
	b, err := os.ReadFile("first_touch.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "WireCohortAuth") {
		t.Fatal("first-touch CLI must WireCohortAuth so IMAP reply ingest can be proven")
	}
	if !strings.Contains(s, "NewPostgresCohortStore") {
		t.Fatal("first-touch CLI must construct NewPostgresCohortStore")
	}
	if strings.Contains(s, "NewMemoryCohortStore") {
		t.Fatal("first-touch CLI must not use NewMemoryCohortStore as cohort authority")
	}
	policy := strings.Index(s, "WirePolicyAuth")
	cohort := strings.Index(s, "WireCohortAuth")
	if policy < 0 || cohort < policy {
		t.Fatal("WireCohortAuth must sit with the rest of first-touch boot wiring")
	}
}
