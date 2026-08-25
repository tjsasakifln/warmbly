package main

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge"
)

func TestValidateFirstTouchCanaryConfigFailsClosed(t *testing.T) {
	orgID := uuid.New()
	sha := strings.Repeat("a", 40)
	valid := confenge.Config{
		OperatorOrgID: orgID, DelegatedFirstTouchAutorunEnabled: true,
		SendingPaused: true, RepositorySHA: sha,
	}
	if err := validateFirstTouchCanaryConfig(valid, orgID, confenge.DelegatedFirstTouchPolicyV1, sha); err != nil {
		t.Fatalf("valid canary config: %v", err)
	}

	tests := []struct {
		name, confirm, expectedSHA string
		orgID                      uuid.UUID
		mutate                     func(*confenge.Config)
	}{
		{name: "wrong policy confirmation", confirm: "HUMAN_APPROVE", expectedSHA: sha, orgID: orgID},
		{name: "wrong organization", confirm: confenge.DelegatedFirstTouchPolicyV1, expectedSHA: sha, orgID: uuid.New()},
		{name: "autorun disabled", confirm: confenge.DelegatedFirstTouchPolicyV1, expectedSHA: sha, orgID: orgID, mutate: func(c *confenge.Config) { c.DelegatedFirstTouchAutorunEnabled = false }},
		{name: "sending open", confirm: confenge.DelegatedFirstTouchPolicyV1, expectedSHA: sha, orgID: orgID, mutate: func(c *confenge.Config) { c.SendingPaused = false }},
		{name: "short release", confirm: confenge.DelegatedFirstTouchPolicyV1, expectedSHA: "abc123", orgID: orgID},
		{name: "non-hex release", confirm: confenge.DelegatedFirstTouchPolicyV1, expectedSHA: strings.Repeat("z", 40), orgID: orgID},
		{name: "release drift", confirm: confenge.DelegatedFirstTouchPolicyV1, expectedSHA: strings.Repeat("b", 40), orgID: orgID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			if tt.mutate != nil {
				tt.mutate(&cfg)
			}
			if err := validateFirstTouchCanaryConfig(cfg, tt.orgID, tt.confirm, tt.expectedSHA); err == nil {
				t.Fatal("unsafe canary config passed")
			}
		})
	}
}
