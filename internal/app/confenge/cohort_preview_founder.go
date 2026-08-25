package confenge

import (
	"fmt"
	"sort"
	"strings"
)

// founderRule separates the two halves of the review: the email, then everything
// that is not the email.
const founderRule = "======================================================================"

// founderSample is a review excerpt plus the frozen member's content hash. The
// hash lives on FrozenCohortMember, not on CohortClassSample, so it is carried
// alongside instead of widening the manifest type.
type founderSample struct {
	CohortClassSample
	ContentHash string
}

// writeFounderReview renders what the founder is actually being asked to judge:
// the message a real person will open. The recipient-facing block comes first
// for every sampled member; internal fields follow, explicitly labelled.
//
// It takes over the builder on purpose. The caller has already written the
// cohort-level technical header, and reading starts with the emails, so that
// text is captured and reprinted at the end under its own heading.
func writeFounderReview(b *strings.Builder, snap *FrozenCohortSnapshot, opts CohortPreviewOptions) {
	technical := strings.TrimRight(b.String(), "\n")
	b.Reset()

	opts = opts.normalized()
	ordered := orderedFounderSamples(snap, opts)

	fmt.Fprintf(b, "%s\n", founderRule)
	if len(ordered) == 0 {
		fmt.Fprintf(b, "EMAIL QUE O DESTINATARIO VAI RECEBER\n%s\n", founderRule)
		fmt.Fprintf(b, "\nnenhuma amostra composta neste snapshot\n")
	} else {
		note := "mailbox redigido; use --show-mailbox para revelar o destino real"
		if opts.ShowMailbox {
			note = "ENDERECOS REAIS DE DESTINO EXIBIDOS"
		}
		fmt.Fprintf(b, "EMAIL QUE O DESTINATARIO VAI RECEBER (%d amostra(s), ate %d por classe de rota)\n", len(ordered), opts.SamplesPerClass)
		fmt.Fprintf(b, "%s\n%s\n", note, founderRule)
		for i := range ordered {
			writeFounderEmail(b, i+1, len(ordered), ordered[i], opts)
		}
	}

	fmt.Fprintf(b, "\n%s\n", founderRule)
	fmt.Fprintf(b, "dados tecnicos do lote (nao fazem parte do email)\n")
	fmt.Fprintf(b, "%s\n", founderRule)
	if technical != "" {
		b.WriteString(technical)
		b.WriteString("\n")
	}
}

// writeFounderEmail prints one sampled message: the recipient-facing block, then
// the internal metadata behind a header that says it is not part of the email.
func writeFounderEmail(b *strings.Builder, idx, total int, s founderSample, opts CohortPreviewOptions) {
	fmt.Fprintf(b, "\n----- EMAIL %d/%d -----------------------------------------------------\n", idx, total)
	fmt.Fprintf(b, "ASSUNTO: %s\n", orAbsent(s.Subject))
	fmt.Fprintf(b, "CORPO:\n")
	if strings.TrimSpace(s.BodyText) == "" {
		fmt.Fprintf(b, "  | %s\n", previewAbsent)
	} else {
		for _, line := range strings.Split(strings.TrimRight(s.BodyText, "\n"), "\n") {
			fmt.Fprintf(b, "  | %s\n", line)
		}
	}
	fmt.Fprintf(b, "----- fim do EMAIL %d/%d ----------------------------------------------\n", idx, total)

	fmt.Fprintf(b, "\n  metadata interna (nao faz parte do email):\n")
	fmt.Fprintf(b, "      route_class=%s account_ref=%s\n", orAbsent(s.RouteClass), orAbsent(s.AccountRef))
	fmt.Fprintf(b, "      company=%s\n", orAbsent(s.Company))
	fmt.Fprintf(b, "      mailbox=%s mailbox_purpose=%s\n", sampleMailbox(s.CohortClassSample, opts.ShowMailbox), orAbsent(s.MailboxPurpose))
	fmt.Fprintf(b, "      greeting=%s person_unknown=%v\n", quoteOrAbsent(s.Greeting), s.PersonUnknown)
	fmt.Fprintf(b, "      fact=%s (source=%s)\n", quoteOrAbsent(s.ObservedFact), orAbsent(s.FactSource))
	// Renderer artifact, not a copy defect: this is the sentence that already
	// closes the body above, shown again as a field.
	fmt.Fprintf(b, "      cta=%s (source=%s) [mesma frase que encerra o CORPO acima; nao e um segundo CTA dentro do email]\n",
		quoteOrAbsent(s.CTA), orAbsent(s.CTASource))
	if strings.TrimSpace(s.ContentHash) != "" {
		fmt.Fprintf(b, "      content_hash=%s\n", s.ContentHash)
	}
	writeReasonList(b, "why_admitted", s.AdmissionReasons)
	writeReasonList(b, "why_this_route", s.RouteReasons)
}

// orderedFounderSamples flattens the per-class sample into one stable reading
// order and attaches each member's content hash where the sample came from the
// frozen members rather than from a read-back preview.
func orderedFounderSamples(snap *FrozenCohortSnapshot, opts CohortPreviewOptions) []founderSample {
	samples := selectPreviewSamples(snap.Members, opts.SamplesPerClass)
	if len(samples) == 0 {
		// A snapshot read back without members is still worth previewing.
		samples = snap.Preview.SamplesByClass
	}
	classes := make([]string, 0, len(samples))
	for k := range samples {
		classes = append(classes, k)
	}
	sort.Strings(classes)

	hashes := make(map[string]string, len(snap.Members))
	for i := range snap.Members {
		m := &snap.Members[i]
		hashes[m.AccountRef+"\x00"+m.Mailbox] = m.ContentHash
	}

	var out []founderSample
	for _, class := range classes {
		for _, s := range samples[class] {
			out = append(out, founderSample{CohortClassSample: s, ContentHash: hashes[s.AccountRef+"\x00"+s.Mailbox]})
		}
	}
	return out
}
