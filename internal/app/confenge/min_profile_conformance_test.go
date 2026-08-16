package confenge

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMinProfileValidateStartupStillRejectsForbiddenAutomation(t *testing.T) {
	base := Config{
		Enabled: true, OperatorMode: true, OperatorUserID: uuid.New(), OperatorOrgID: uuid.New(),
		RequireHumanApproval: true, DefaultDailyLimit: 200, MaxInitialEmailWords: 120,
	}
	t.Setenv("APP_URL", "http://127.0.0.1:5173")

	auto := base
	auto.AutoSendEnabled = true
	if err := auto.ValidateStartup("production"); err == nil {
		t.Fatal("CONFENGE_AUTO_SEND_ENABLED=true must fail closed")
	}
	noHuman := base
	noHuman.RequireHumanApproval = false
	if err := noHuman.ValidateStartup("production"); err == nil {
		t.Fatal("CONFENGE_REQUIRE_HUMAN_APPROVAL=false must fail closed")
	}
	green := base
	green.GreenAutorunEnabled = true
	if err := green.ValidateStartup("production"); err == nil {
		t.Fatal("CONFENGE_GREEN_AUTORUN_ENABLED=true must fail closed")
	}
}

func TestMinProfileComposeAndEnvDeclareSameAllowedInfra(t *testing.T) {
	files := []string{
		"docker-compose.yml",
		"docker-compose.confenge.yml",
		"deploy/confenge-vps/docker-compose.override.yml",
		"deploy/confenge-vps/env.example",
		"Makefile",
	}
	contents := map[string]string{}
	for _, f := range files {
		contents[f] = readRepoFile(t, f)
	}

	base := contents["docker-compose.yml"]
	for _, pair := range [][2]string{
		{"EVENTBUS_PROVIDER", "nats"},
		{"KMS_PROVIDER", "local"},
		{"BLOB_PROVIDER", "filesystem"},
		{"TASKS_PROVIDER", "local"},
		{"BILLING_PROVIDER", "none"},
	} {
		if !strings.Contains(base, pair[0]+": ${"+pair[0]+":-"+pair[1]+"}") &&
			!strings.Contains(base, pair[0]+": ${"+pair[0]+":-"+pair[1]+"}") {
			if !strings.Contains(base, pair[0]) || !strings.Contains(base, pair[1]) {
				t.Fatalf("docker-compose.yml must default %s=%s", pair[0], pair[1])
			}
		}
	}

	makeFile := contents["Makefile"]
	for _, needle := range []string{
		"KMS_PROVIDER=local",
		"BLOB_PROVIDER=filesystem",
		"EVENTBUS_PROVIDER=nats",
		"TASKS_PROVIDER=local",
		"BILLING_PROVIDER=none",
		"GO_MINPROFILE_TAGS",
	} {
		if !strings.Contains(makeFile, needle) {
			t.Fatalf("Makefile missing %q", needle)
		}
	}

	overlay := contents["docker-compose.confenge.yml"]
	if !strings.Contains(overlay, "GO_TAGS") || !strings.Contains(overlay, "minprofile") {
		t.Fatal("docker-compose.confenge.yml must build with GO_TAGS=minprofile")
	}
	if strings.Contains(overlay, "kafka:") || strings.Contains(overlay, "stripe-mock") || strings.Contains(overlay, "localstack") {
		t.Fatal("min compose overlay must not add kafka/stripe/localstack services")
	}

	vps := contents["deploy/confenge-vps/docker-compose.override.yml"]
	if !strings.Contains(vps, "minprofile") {
		t.Fatal("VPS overlay must pass GO_TAGS=minprofile")
	}
	if !strings.Contains(vps, "CONFENGE_AUTO_SEND_ENABLED: ${CONFENGE_AUTO_SEND_ENABLED:-false}") {
		t.Fatal("VPS overlay must keep AUTO_SEND default false")
	}
	if !strings.Contains(vps, "CONFENGE_REQUIRE_HUMAN_APPROVAL: ${CONFENGE_REQUIRE_HUMAN_APPROVAL:-true}") {
		t.Fatal("VPS overlay must keep human approval default true")
	}
	if !strings.Contains(vps, "CONFENGE_GREEN_AUTORUN_ENABLED: ${CONFENGE_GREEN_AUTORUN_ENABLED:-false}") {
		t.Fatal("VPS overlay must keep green autorun default false")
	}

	envEx := contents["deploy/confenge-vps/env.example"]
	for _, needle := range []string{
		"CONFENGE_AUTO_SEND_ENABLED=false",
		"CONFENGE_REQUIRE_HUMAN_APPROVAL=true",
		"CONFENGE_GREEN_AUTORUN_ENABLED=false",
	} {
		if !strings.Contains(envEx, needle) {
			t.Fatalf("VPS env.example missing %q", needle)
		}
	}
	for _, banned := range []string{
		"EVENTBUS_PROVIDER=kafka",
		"KMS_PROVIDER=aws",
		"BLOB_PROVIDER=s3",
		"TASKS_PROVIDER=gcloud",
		"BILLING_PROVIDER=stripe",
		"AWS_ACCESS_KEY_ID=",
		"STRIPE_SECRET_KEY=",
	} {
		if strings.Contains(envEx, banned) {
			t.Fatalf("VPS env.example must not require %q", banned)
		}
	}
}
