package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/app/confenge"
	"github.com/warmbly/warmbly/internal/app/confenge/intel"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
	"github.com/warmbly/warmbly/internal/seed"
)

func cmdCohort(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: confenge cohort prepare|preview|authorize|review|report [flags]")
		return 2
	}
	switch args[0] {
	case "prepare":
		return cmdCohortPrepare(args[1:])
	case "preview":
		return cmdCohortPreview(args[1:])
	case "authorize":
		return cmdCohortAuthorize(args[1:])
	case "review":
		return cmdCohortReview(args[1:])
	case "report":
		return cmdCohortReport(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown cohort command %q\n", args[0])
		return 2
	}
}

func cmdCohortPrepare(args []string) int {
	fs := flag.NewFlagSet("cohort prepare", flag.ExitOnError)
	feedPath := fs.String("feed", "", "extra-cli feed JSON (preferred; hashes are derived)")
	outPath := fs.String("out", "", "write frozen snapshot JSON to PATH")
	orgStr := fs.String("org-id", "", "load accounts from postgres instead of --feed")
	limit := fs.Int("limit", confenge.DefaultCohortLimit, "max accounts in the frozen set (1-100)")
	volume := fs.Int("max-daily", confenge.DefaultCohortDailyVolume, "max daily volume bound into the grant")
	ttl := fs.String("ttl", "24h", "authorization TTL")
	sha := fs.String("repository-sha", "", "deployed SHA (defaults to CONFENGE_REPOSITORY_SHA)")
	_ = fs.Parse(args)

	dur, err := time.ParseDuration(*ttl)
	if err != nil || dur <= 0 {
		fmt.Fprintf(os.Stderr, "BLOCKED: invalid --ttl %q\n", *ttl)
		return 2
	}
	maybeLoadDotEnv()
	cfg := confenge.LoadConfig()
	opts := confenge.CohortPrepareOptions{
		Now:               time.Now().UTC(),
		Limit:             *limit,
		MaxDailyVolume:    *volume,
		TTL:               dur,
		RepositorySHA:     firstFlag(*sha, cfg.RepositorySHA),
		FeedSchemaVersion: firstNonEmpty(cfg.FeedSchemaVersion, models.OutreachSchemaV1),
		EvidenceVersion:   firstNonEmpty(cfg.EvidenceVersion, confenge.DefaultEvidenceVersion),
		ComposerVersion:   confenge.ComposerVersion,
		PolicyVersion:     confenge.BoundedCohortPolicyV1,
	}

	var snap *confenge.FrozenCohortSnapshot
	switch {
	case strings.TrimSpace(*feedPath) != "":
		raw, err := os.ReadFile(*feedPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "BLOCKED: feed: %v\n", err)
			return 1
		}
		feed, err := confenge.ParseFeed(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "BLOCKED: parse feed: %v\n", err)
			return 1
		}
		snap, err = confenge.PrepareControlledCohortFromFeed(feed, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "BLOCKED: prepare: %v\n", err)
			return 1
		}
	case strings.TrimSpace(*orgStr) != "":
		orgID := parseOrg(*orgStr)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pool, err := pgxpool.New(ctx, primaryDSN())
		if err != nil {
			fmt.Fprintf(os.Stderr, "BLOCKED: postgres: %v\n", err)
			return 1
		}
		defer pool.Close()
		repo := repository.NewOutreachRepository(pool)
		accounts, err := confenge.AccountsFromOrg(ctx, repo, orgID, "extra-cli")
		if err != nil {
			fmt.Fprintf(os.Stderr, "BLOCKED: load accounts: %v\n", err)
			return 1
		}
		snap, err = confenge.PrepareControlledCohort(accounts, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "BLOCKED: prepare: %v\n", err)
			return 1
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: confenge cohort prepare --feed PATH [--out PATH] or --org-id UUID")
		return 2
	}

	fmt.Print(confenge.FormatCohortPreview(snap))
	enc, err := confenge.MarshalFrozenCohort(snap)
	if err != nil {
		fmt.Fprintf(os.Stderr, "BLOCKED: marshal: %v\n", err)
		return 1
	}
	if *outPath != "" && *outPath != "-" {
		if err := os.WriteFile(*outPath, enc, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "BLOCKED: write: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "wrote frozen snapshot %s\n", *outPath)
	} else if *outPath == "-" {
		fmt.Println(string(enc))
	}
	return 0
}

func cmdCohortPreview(args []string) int {
	fs := flag.NewFlagSet("cohort preview", flag.ExitOnError)
	manifest := fs.String("manifest", "", "frozen snapshot JSON from cohort prepare --out")
	asJSON := fs.Bool("json", false, "print the snapshot JSON")
	_ = fs.Parse(args)
	snap, err := readFrozenManifest(*manifest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "BLOCKED: %v\n", err)
		return 2
	}
	if *asJSON {
		enc, _ := confenge.MarshalFrozenCohort(snap)
		fmt.Println(string(enc))
		return 0
	}
	fmt.Print(confenge.FormatCohortPreview(snap))
	return 0
}

func cmdCohortAuthorize(args []string) int {
	fs := flag.NewFlagSet("cohort authorize", flag.ExitOnError)
	manifest := fs.String("manifest", "", "frozen snapshot JSON")
	actor := fs.String("actor", "", "human actor UUID (required)")
	orgStr := fs.String("org-id", seed.DevOrgID.String(), "organization UUID")
	confirm := fs.Bool("confirm", false, "persist the grant and apply to touchpoints")
	_ = fs.Parse(args)
	if strings.TrimSpace(*actor) == "" {
		fmt.Fprintln(os.Stderr, "BLOCKED: --actor (human UUID) is required")
		return 2
	}
	actorID, err := uuid.Parse(strings.TrimSpace(*actor))
	if err != nil || actorID == uuid.Nil {
		fmt.Fprintln(os.Stderr, "BLOCKED: --actor must be a real non-zero UUID")
		return 2
	}
	snap, err := readFrozenManifest(*manifest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "BLOCKED: %v\n", err)
		return 2
	}
	orgID := parseOrg(*orgStr)
	now := time.Now().UTC()
	if !*confirm {
		auth, err := confenge.GrantFromFrozenSnapshot(snap, orgID, actorID, now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "BLOCKED: %v\n", err)
			return 1
		}
		enc, _ := json.MarshalIndent(auth.Summary(), "", "  ")
		fmt.Println(string(enc))
		fmt.Print(confenge.FormatCohortPreview(snap))
		fmt.Fprintln(os.Stderr, "not persisted. Re-run with --confirm after reviewing the summary.")
		return 0
	}
	pool, store, err := openCohortStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "BLOCKED: %v\n", err)
		return 1
	}
	defer pool.Close()
	repo := repository.NewOutreachRepository(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := confenge.AuthorizeFrozenCohort(ctx, store, repo, orgID, actorID, snap, true, now)
	if res != nil {
		fmt.Print(confenge.FormatCohortAuthorize(res))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "BLOCKED: %v\n", err)
		return 1
	}
	return 0
}

func cmdCohortReview(args []string) int {
	fs := flag.NewFlagSet("cohort review", flag.ExitOnError)
	idStr := fs.String("id", "", "authorization UUID")
	actor := fs.String("actor", "", "human actor UUID")
	verdict := fs.String("verdict", confenge.ReleaseReadyForControlledEmailReview, "READY_FOR_CONTROLLED_EMAIL_GO_REVIEW or NO_GO")
	reason := fs.String("reason", "", "review note")
	confirm := fs.Bool("confirm", false, "persist the human decision")
	_ = fs.Parse(args)
	id, err := uuid.Parse(strings.TrimSpace(*idStr))
	if err != nil || id == uuid.Nil {
		fmt.Fprintln(os.Stderr, "usage: confenge cohort review --id UUID --actor UUID [--verdict READY_FOR_CONTROLLED_EMAIL_GO_REVIEW|NO_GO] [--confirm]")
		return 2
	}
	actorID, err := uuid.Parse(strings.TrimSpace(*actor))
	if err != nil || actorID == uuid.Nil {
		fmt.Fprintln(os.Stderr, "BLOCKED: --actor (human UUID) is required")
		return 2
	}
	pool, store, err := openCohortStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "BLOCKED: %v\n", err)
		return 1
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	now := time.Now().UTC()
	cfg := confenge.LoadConfig()
	cmp, auth, err := confenge.PrepareControlledEmailGOReview(ctx, confenge.LiveReleaseInput{
		Now:    now,
		Config: &cfg,
		Store:  store,
		Repo:   repository.NewOutreachRepository(pool),
	}, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "BLOCKED: %v\n", err)
		return 1
	}
	fmt.Print(confenge.FormatReleaseComparison(cmp))
	if !*confirm {
		fmt.Fprintln(os.Stderr, "not persisted; rerun with --confirm")
		return 0
	}
	live := confenge.ReleaseManifest{}
	if cmp != nil {
		live = cmp.Got
	}
	auth, err = confenge.RecordControlledEmailGOReview(ctx, store, id, actorID, *verdict, *reason, live, now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "BLOCKED: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "review recorded authorization_id=%s verdict=%s (not GO_FOR_CONTROLLED_EMAIL_PILOT; auto_send remains false)\n",
		auth.ID, auth.GOReviewVerdict)
	enc, _ := json.MarshalIndent(auth.Summary(), "", "  ")
	fmt.Println(string(enc))
	return 0
}

func cmdCohortReport(args []string) int {
	fs := flag.NewFlagSet("cohort report", flag.ExitOnError)
	eventsPath := fs.String("events", "", "JSON array of commercial events")
	idStr := fs.String("id", "", "authorization UUID (membership labels UNKNOWN slices)")
	_ = fs.Parse(args)
	var events []intel.CommercialEvent
	if strings.TrimSpace(*eventsPath) != "" {
		raw, err := os.ReadFile(*eventsPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "BLOCKED: events: %v\n", err)
			return 1
		}
		if err := json.Unmarshal(raw, &events); err != nil {
			fmt.Fprintf(os.Stderr, "BLOCKED: events json: %v\n", err)
			return 1
		}
	}
	if strings.TrimSpace(*idStr) != "" && len(events) == 0 {
		fmt.Fprintln(os.Stderr, "no events supplied; slices will be empty. Pass --events from the observe path.")
	}
	rep := intel.BuildControlledEmailExecutiveReport(events)
	fmt.Print(intel.FormatControlledEmailReport(rep))
	return 0
}

func readFrozenManifest(path string) (*confenge.FrozenCohortSnapshot, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("usage requires --manifest PATH")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return confenge.UnmarshalFrozenCohort(raw)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
