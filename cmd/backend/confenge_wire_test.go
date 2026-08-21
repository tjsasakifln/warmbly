package main

import (
	"os"
	"strings"
	"testing"
)

func TestBackendWiresCohortAuthAtBoot(t *testing.T) {
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	policy := strings.Index(s, "WirePolicyAuth")
	cohort := strings.Index(s, "WireCohortAuth")
	if policy < 0 {
		t.Fatal("backend must WirePolicyAuth")
	}
	if cohort < 0 {
		t.Fatal("backend must WireCohortAuth so bounded cohort grants exist at transport")
	}
	if cohort < policy {
		t.Fatal("WireCohortAuth must be called with the rest of CONFENGE boot wiring")
	}
	if !strings.Contains(s, "NewPostgresCohortStore") {
		t.Fatal("backend production boot must construct NewPostgresCohortStore")
	}
	if strings.Contains(s, "NewMemoryCohortStore") {
		t.Fatal("backend production boot must not use NewMemoryCohortStore as cohort authority")
	}
}
