package intel

import (
	"encoding/json"
)

// FrozenCatalogAuthorityHash is the default authority pin for the frozen
// #47 / web-cfg#88 / Governance#1 offer contract. Override with
// CONFENGE_CATALOG_AUTHORITY_HASH.
const FrozenCatalogAuthorityHash = "9c3b6f1a2d8e0475b1c4a6d0e8f23791c5a0b4d6e1f2083a7c9d5e4b2a1806f3"

// FrozenOffer returns the versioned public catalog row. Extra is never here.
func FrozenOffer(id string) (OfferSnapshot, bool) {
	switch id {
	case OfferDiagnostico:
		o := OfferSnapshot{
			OfferID:              OfferDiagnostico,
			OfferVersion:         "v1",
			PublicCode:           OfferDiagnostico,
			InternalCode:         OfferDiagnostico,
			TermsVersion:         "CFG-TERMS-B2B-2026-08-17-v1",
			AmountCents:          800000,
			Currency:             CurrencyBRL,
			BillingMode:          BillingOneTime,
			Cycle:                "once",
			CommitmentMonths:     0,
			MaxPayments:          1,
			TotalCommitmentCents: 800000,
			NoticeDays:           0,
			ScopeVersion:         "diagnostico-exp-v1",
			CatalogAuthorityHash: CatalogAuthorityHash(),
			TaxPremisePercent:    TaxPremisePercent,
			CanonicalAPIHost:     CanonicalAPIHost,
			Public:               true,
		}
		o.TermsHash = "terms:" + o.TermsVersion
		o.SnapshotHash = HashOfferSnapshot(o)
		return o, true
	case OfferDirB2G180:
		o := OfferSnapshot{
			OfferID:              OfferDirB2G180,
			OfferVersion:         "v1",
			PublicCode:           OfferDirB2G180,
			InternalCode:         OfferDirB2G180,
			TermsVersion:         "CFG-TERMS-B2B-2026-08-17-v1",
			AmountCents:          1500000,
			Currency:             CurrencyBRL,
			BillingMode:          BillingRecurring,
			Cycle:                "monthly",
			CommitmentMonths:     6,
			MaxPayments:          6,
			TotalCommitmentCents: 9000000,
			NoticeDays:           30,
			ScopeVersion:         "direcao-b2g-180-v1",
			CatalogAuthorityHash: CatalogAuthorityHash(),
			TaxPremisePercent:    TaxPremisePercent,
			CanonicalAPIHost:     CanonicalAPIHost,
			Public:               true,
		}
		o.TermsHash = "terms:" + o.TermsVersion
		o.SnapshotHash = HashOfferSnapshot(o)
		return o, true
	default:
		return OfferSnapshot{}, false
	}
}

// FrozenCatalogJSON is the secret-free catalog fixture.
func FrozenCatalogJSON() []byte {
	diag, _ := FrozenOffer(OfferDiagnostico)
	dir, _ := FrozenOffer(OfferDirB2G180)
	payload := map[string]any{
		"schema":         "confenge.catalog.freeze.v1",
		"authority_hash": CatalogAuthorityHash(),
		"notes":          "Extra R$10k is a private historical exception and is not a catalog row",
		"offers":         []OfferSnapshot{diag, dir},
	}
	raw, _ := json.Marshal(payload)
	return raw
}
