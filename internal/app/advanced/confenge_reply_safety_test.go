package advanced

import "testing"

func TestConfengeReplyNeverLaunchesInstantFollowUp(t *testing.T) {
	for _, name := range []string{"CONFENGE | Outreach consultivo inicial", " confenge pilot"} {
		if allowsInstantReplyActions(name) {
			t.Fatalf("CONFENGE reply allowed instant follow-up for %q", name)
		}
	}
	if !allowsInstantReplyActions("Customer onboarding") {
		t.Fatal("unrelated campaign behavior changed")
	}
}
