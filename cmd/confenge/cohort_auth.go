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
	"github.com/warmbly/warmbly/internal/models"
)

func cmdCohortAuth(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: confenge cohort-auth create|show|revoke [flags]")
		return 2
	}
	switch args[0] {
	case "create":
		return cmdCohortAuthCreate(args[1:])
	case "show":
		return cmdCohortAuthShow(args[1:])
	case "revoke":
		return cmdCohortAuthRevoke(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown cohort-auth command %q\n", args[0])
		return 2
	}
}

func openCohortStore() (*pgxpool.Pool, confenge.BoundedCohortStore, error) {
	maybeLoadDotEnv()
	dsn := primaryDSN()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres required for bounded cohort authority: %w", err)
	}
	return pool, confenge.NewPostgresCohortStore(pool), nil
}

func cmdCohortAuthCreate(args []string) int {
	fs := flag.NewFlagSet("cohort-auth create", flag.ExitOnError)
	actor := fs.String("actor", "", "human actor UUID (required)")
	org := fs.String("org-id", "", "organization UUID")
	cohortID := fs.String("cohort-id", "", "frozen cohort id")
	cohortHash := fs.String("cohort-hash", "", "frozen cohort hash")
	recipientHash := fs.String("recipient-set-hash", "", "hash of the frozen recipient set")
	sha := fs.String("repository-sha", "", "deployed repository SHA")
	schema := fs.String("feed-schema", "", "extra-cli feed schema version")
	policy := fs.String("policy-version", confenge.BoundedCohortPolicyV1, "bounded cohort policy version")
	composer := fs.String("composer-version", "", "composer version")
	evidence := fs.String("evidence-version", "", "evidence version")
	classes := fs.String("allowed-route-classes", "DIRECT_PERSON,ROLE_OR_DEPARTMENT,GENERIC_COMPANY,PUBLIC_COMPANY_FREEMAIL", "comma-separated route classes")
	volume := fs.Int("max-daily-volume", 50, "max daily volume (1-100)")
	ttl := fs.String("ttl", "24h", "authorization TTL")
	confirm := fs.Bool("confirm", false, "persist the grant after printing the summary")
	synthetic := fs.Bool("synthetic", false, "mark this grant as a synthetic/test grant")
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
	orgID := uuid.Nil
	if strings.TrimSpace(*org) != "" {
		orgID = parseOrg(*org)
	}
	dur, err := time.ParseDuration(*ttl)
	if err != nil || dur <= 0 {
		fmt.Fprintf(os.Stderr, "BLOCKED: invalid --ttl %q\n", *ttl)
		return 2
	}
	cfg := confenge.LoadConfig()
	var classList []string
	for _, c := range strings.Split(*classes, ",") {
		c = strings.TrimSpace(c)
		if c != "" {
			classList = append(classList, c)
		}
	}
	now := time.Now().UTC()
	auth := &confenge.BoundedCohortAuthorization{
		ID: uuid.New(), OrganizationID: orgID, ActorID: actorID, AuthorizedAt: now,
		RepositorySHA:     firstFlag(*sha, cfg.RepositorySHA),
		FeedSchemaVersion: firstFlag(*schema, cfg.FeedSchemaVersion, models.OutreachSchemaV1),
		CohortID:          strings.TrimSpace(*cohortID), CohortHash: strings.TrimSpace(*cohortHash),
		PolicyVersion: *policy, AllowedRouteClasses: classList, MaxDailyVolume: *volume,
		RecipientSetHash: strings.TrimSpace(*recipientHash),
		ComposerVersion:  firstFlag(*composer, confenge.ComposerVersion),
		EvidenceVersion:  firstFlag(*evidence, cfg.EvidenceVersion, confenge.DefaultEvidenceVersion),
		TTL:              dur, ExpiresAt: now.Add(dur),
	}
	if err := confenge.NormalizeBoundedCohortGrant(auth, now); err != nil {
		fmt.Fprintf(os.Stderr, "BLOCKED: %v\n", err)
		return 1
	}
	sum := auth.Summary()
	enc, _ := json.MarshalIndent(sum, "", "  ")
	fmt.Println(string(enc))
	if *synthetic {
		fmt.Fprintln(os.Stderr, "synthetic=true (test grant; not a real prospect cohort)")
	}
	if !*confirm {
		fmt.Fprintln(os.Stderr, "not persisted. Re-run with --confirm after reviewing the summary.")
		return 0
	}
	pool, store, err := openCohortStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "BLOCKED: %v\n", err)
		return 1
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	got, err := confenge.CreateBoundedCohortGrant(ctx, store, auth, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "BLOCKED: persist: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "persisted authorization_id=%s frozen_hash=%s\n", got.ID, got.FrozenHash())
	return 0
}

func cmdCohortAuthShow(args []string) int {
	fs := flag.NewFlagSet("cohort-auth show", flag.ExitOnError)
	idStr := fs.String("id", "", "authorization UUID")
	_ = fs.Parse(args)
	id, err := uuid.Parse(strings.TrimSpace(*idStr))
	if err != nil || id == uuid.Nil {
		fmt.Fprintln(os.Stderr, "usage: confenge cohort-auth show --id UUID")
		return 2
	}
	pool, store, err := openCohortStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "BLOCKED: %v\n", err)
		return 1
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	auth, err := confenge.ViewBoundedCohortGrant(ctx, store, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "BLOCKED: %v\n", err)
		return 1
	}
	enc, _ := json.MarshalIndent(auth.Summary(), "", "  ")
	fmt.Println(string(enc))
	return 0
}

func cmdCohortAuthRevoke(args []string) int {
	fs := flag.NewFlagSet("cohort-auth revoke", flag.ExitOnError)
	idStr := fs.String("id", "", "authorization UUID")
	actor := fs.String("actor", "", "human actor UUID")
	reason := fs.String("reason", "", "revoke reason (required)")
	_ = fs.Parse(args)
	id, err := uuid.Parse(strings.TrimSpace(*idStr))
	if err != nil || id == uuid.Nil {
		fmt.Fprintln(os.Stderr, "usage: confenge cohort-auth revoke --id UUID --actor UUID --reason TEXT")
		return 2
	}
	actorID, err := uuid.Parse(strings.TrimSpace(*actor))
	if err != nil || actorID == uuid.Nil {
		fmt.Fprintln(os.Stderr, "BLOCKED: --actor (human UUID) is required")
		return 2
	}
	if strings.TrimSpace(*reason) == "" {
		fmt.Fprintln(os.Stderr, "BLOCKED: --reason is required")
		return 2
	}
	pool, store, err := openCohortStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "BLOCKED: %v\n", err)
		return 1
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := confenge.RevokeBoundedCohortGrant(ctx, store, id, actorID, *reason, time.Now().UTC()); err != nil {
		fmt.Fprintf(os.Stderr, "BLOCKED: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "revoked authorization_id=%s actor=%s reason=%s\n", id, actorID, *reason)
	return 0
}

func firstFlag(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
