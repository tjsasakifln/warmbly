package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/app/confenge"
)

// qualificationRecord is one line of the COMMERCIAL_AUTHORITY/2.0 corpus
// produced from already-stored public-contract evidence. Rebuilding membership
// never requires a new PNCP crawl: the qualifying facts are already recorded.
type qualificationRecord struct {
	confenge.RootQualification
}

// cmdCommercialQualification applies a V2 qualification corpus to the accounts
// of one organization. It is idempotent: replaying the same corpus rewrites
// identical values, and a restart mid-run converges because every row is
// derived purely from its own evidence line.
func cmdCommercialQualification(args []string) int {
	fs := flag.NewFlagSet("commercial-qualification", flag.ContinueOnError)
	corpusPath := fs.String("corpus", "", "path to the JSONL qualification corpus (one RootQualification per line)")
	orgFlag := fs.String("org-id", "", "organization UUID (defaults to CONFENGE_ORG_ID)")
	dryRun := fs.Bool("dry-run", false, "report the plan without writing")
	batchSize := fs.Int("batch", 500, "rows per transaction")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*corpusPath) == "" {
		fmt.Fprintln(os.Stderr, "--corpus is required")
		return 2
	}
	orgID, err := resolveOrgID(*orgFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	records, err := readQualificationCorpus(*corpusPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "corpus: %v\n", err)
		return 1
	}
	now := time.Now().UTC()

	// Classify before touching anything so the operator sees the real plan.
	byRoot := make(map[string]confenge.RootQualification, len(records))
	counts := map[string]int{}
	for _, rec := range records {
		decision := confenge.EvaluateRootQualification(&rec.RootQualification, now)
		counts[decision.State]++
		if prev, ok := byRoot[rec.CNPJRoot8]; ok {
			// Several qualifying contracts: the company stays active while any
			// one is inside the window, so keep the latest contracting act.
			if prev.QualifiedUntil >= rec.QualifiedUntil {
				continue
			}
		}
		byRoot[rec.CNPJRoot8] = rec.RootQualification
	}
	roots := make([]string, 0, len(byRoot))
	for root := range byRoot {
		roots = append(roots, root)
	}
	sort.Strings(roots)

	corpus := make([]confenge.RootQualification, 0, len(roots))
	for _, root := range roots {
		corpus = append(corpus, byRoot[root])
	}
	fmt.Printf("corpus_lines=%d distinct_roots=%d qualified=%d expired=%d revoked=%d unknown=%d corpus_hash=%s\n",
		len(records), len(roots), counts[confenge.CommercialQualified], counts[confenge.CommercialExpired],
		counts[confenge.CommercialRevoked], counts[confenge.CommercialUnknown],
		confenge.HashQualificationCorpus(corpus))

	// A dry run is a pure evaluation of the corpus and needs no database, so
	// the plan can be audited before any credential is in play.
	ctx := context.Background()
	var pool *pgxpool.Pool
	if !*dryRun {
		p, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "database: %v\n", err)
			return 1
		}
		defer p.Close()
		pool = p
	}

	applied, missing, skipped := 0, 0, 0
	for start := 0; start < len(roots); start += *batchSize {
		end := start + *batchSize
		if end > len(roots) {
			end = len(roots)
		}
		for _, root := range roots[start:end] {
			q := byRoot[root]
			decision := confenge.EvaluateRootQualification(&q, now)
			if !decision.Present {
				skipped++
				continue
			}
			if *dryRun {
				applied++
				continue
			}
			tag, execErr := pool.Exec(ctx, `
				UPDATE outreach_accounts SET
					commercial_qualification_state = $3,
					commercial_qualification_policy_version = $4,
					commercial_qualification_cnpj_root8 = $5,
					commercial_qualifying_contract_id = $6,
					commercial_qualifying_contract_date = $7,
					commercial_qualifying_date_field = $8,
					commercial_qualifying_contract_count = $9,
					commercial_qualified_until = $10,
					commercial_qualification_evidence_hash = $11,
					commercial_qualification_evidence_reference = $12,
					commercial_qualification_provenance = $13,
					commercial_qualification_deactivated = $14,
					commercial_qualification_deactivation_reason = $15,
					commercial_qualification_observed_at = $16,
					updated_at = now()
				WHERE organization_id = $1
				  AND left(regexp_replace(cnpj14, '[^0-9]', '', 'g'), 8) = $2`,
				orgID, root, decision.State, confenge.CommercialAuthorityPolicyV2, q.CNPJRoot8,
				q.QualifyingContractID, decision.QualifyingContractDate, q.QualifyingDateField,
				q.QualifyingContractCount, decision.QualifiedUntil, q.EvidenceHash,
				q.EvidenceReference, q.Provenance, q.Deactivated, q.DeactivationReason, now)
			if execErr != nil {
				fmt.Fprintf(os.Stderr, "apply %s: %v\n", root, execErr)
				return 1
			}
			if tag.RowsAffected() == 0 {
				missing++
				continue
			}
			applied++
		}
		fmt.Printf("progress applied=%d missing=%d skipped=%d of %d roots\n", applied, missing, skipped, len(roots))
	}
	fmt.Printf("commercial-qualification done org=%s applied=%d roots_without_account=%d refused=%d dry_run=%t\n",
		orgID, applied, missing, skipped, *dryRun)
	return 0
}

func readQualificationCorpus(path string) ([]qualificationRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var out []qualificationRecord
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<22)
	line := 0
	for scanner.Scan() {
		line++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var rec qualificationRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if strings.TrimSpace(rec.CNPJRoot8) == "" {
			return nil, fmt.Errorf("line %d: cnpj_root8 is required", line)
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func resolveOrgID(flagValue string) (uuid.UUID, error) {
	raw := strings.TrimSpace(flagValue)
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("CONFENGE_ORG_ID"))
	}
	if raw == "" {
		return uuid.Nil, fmt.Errorf("--org-id or CONFENGE_ORG_ID is required")
	}
	return uuid.Parse(raw)
}
