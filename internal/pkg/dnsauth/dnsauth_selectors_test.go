package dnsauth

import (
	"slices"
	"testing"
)

// Hostinger's real selectors are hostingermail-a/-b/-c. The numeric spellings
// that used to stand in for them match nothing, so a Hostinger-hosted domain
// probed as "no DKIM" while it was signing correctly the whole time —
// confenge.com.br being the case that surfaced it.
func TestDefaultSelectorsCoverHostingerRealFormat(t *testing.T) {
	for _, want := range []string{"hostingermail-a", "hostingermail-b", "hostingermail-c"} {
		if !slices.Contains(defaultSelectors, want) {
			t.Fatalf("defaultSelectors is missing %q; a Hostinger domain would probe as having no DKIM", want)
		}
	}
}

func TestDefaultSelectorsHaveNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range defaultSelectors {
		if seen[s] {
			t.Fatalf("duplicate selector %q costs a DNS lookup per check for nothing", s)
		}
		seen[s] = true
	}
}
