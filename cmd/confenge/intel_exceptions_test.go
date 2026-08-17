package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
)

func TestIntelExceptionsCLIListShowResolveTwice(t *testing.T) {
	var out1, err1 bytes.Buffer
	if code := runIntelExceptions([]string{"list", "--fixture", "--format", "json", "--type", "orphan"}, &out1, &err1); code != 0 {
		t.Fatalf("list1 code=%d stderr=%s", code, err1.String())
	}
	var wrap1 struct {
		Data []intel.Exception `json:"data"`
	}
	if err := json.Unmarshal(out1.Bytes(), &wrap1); err != nil {
		t.Fatalf("list1 json: %v body=%s", err, out1.String())
	}
	if len(wrap1.Data) == 0 {
		t.Fatal("list1 empty")
	}
	ex := wrap1.Data[0]
	if ex.Code != intel.ExceptionOrphan || ex.Lane == "" || ex.Source == "" || ex.Severity == "" {
		t.Fatalf("list item incomplete: %+v", ex)
	}
	if ex.NextAction == "" && ex.Status != intel.StatusOpen {
		t.Fatal("list item has no next action and is not open")
	}

	var out2, err2 bytes.Buffer
	if code := runIntelExceptions([]string{"list", "--fixture", "--format", "json", "--type", "orphan"}, &out2, &err2); code != 0 {
		t.Fatalf("list2 code=%d stderr=%s", code, err2.String())
	}
	if out1.String() != out2.String() {
		t.Fatalf("two fixture list runs disagreed\nrun1=%s\nrun2=%s", out1.String(), out2.String())
	}

	var show1, showErr bytes.Buffer
	if code := runIntelExceptions([]string{"show", ex.ID, "--fixture", "--format", "json"}, &show1, &showErr); code != 0 {
		t.Fatalf("show code=%d stderr=%s", code, showErr.String())
	}
	if !strings.Contains(show1.String(), `"evidence"`) || !strings.Contains(show1.String(), `"history"`) {
		t.Fatalf("show missing evidence/history: %s", show1.String())
	}

	missing, _ := listFixture(t, "missing_version")
	var res1, resErr bytes.Buffer
	args := []string{
		"resolve", missing.ID, "--fixture", "--format", "json",
		"--action", "defer", "--actor", "cli-op", "--reason", "wait for extra-cli version",
		"--idempotency-key", "cli-defer-1",
	}
	if code := runIntelExceptions(args, &res1, &resErr); code != 0 {
		t.Fatalf("resolve code=%d stderr=%s stdout=%s", code, resErr.String(), res1.String())
	}
	var resolved struct {
		Data intel.ResolveResult `json:"data"`
	}
	if err := json.Unmarshal(res1.Bytes(), &resolved); err != nil {
		t.Fatalf("resolve json: %v body=%s", err, res1.String())
	}
	if resolved.Data.After.Status != intel.StatusDeferred || resolved.Data.Actor != "cli-op" || resolved.Data.Reason == "" {
		t.Fatalf("resolve payload: %+v", resolved.Data)
	}
	if resolved.Data.Before.Status != intel.StatusOpen {
		t.Fatalf("before status=%s", resolved.Data.Before.Status)
	}

	var blocked, blockedErr bytes.Buffer
	if code := runIntelExceptions([]string{"list", "--format", "json"}, &blocked, &blockedErr); code == 0 {
		t.Fatal("list without PRIMARY_DB and without --fixture must fail closed")
	}
	if !strings.Contains(blockedErr.String(), "BLOCKED") || !strings.Contains(blockedErr.String(), "PRIMARY_DB") {
		t.Fatalf("expected BLOCKED PRIMARY_DB, got %s", blockedErr.String())
	}
}

func listFixture(t *testing.T, typ string) (intel.Exception, []intel.Exception) {
	t.Helper()
	var out, errBuf bytes.Buffer
	if code := runIntelExceptions([]string{"list", "--fixture", "--format", "json", "--type", typ}, &out, &errBuf); code != 0 {
		t.Fatalf("list %s: %s", typ, errBuf.String())
	}
	var wrap struct {
		Data []intel.Exception `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &wrap); err != nil {
		t.Fatal(err)
	}
	if len(wrap.Data) == 0 {
		t.Fatalf("no %s fixtures", typ)
	}
	return wrap.Data[0], wrap.Data
}
