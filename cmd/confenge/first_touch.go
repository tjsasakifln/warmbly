package main

import (
	"bytes"
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
	"github.com/warmbly/warmbly/internal/app/orgrisk"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

func cmdFirstTouch(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "first-touch requires authorize-policy, seal, apply, or status")
		return 2
	}
	switch args[0] {
	case "authorize-policy":
		return cmdFirstTouchAuthorizePolicy(args[1:])
	case "seal":
		return cmdFirstTouchSeal(args[1:])
	case "apply":
		return cmdFirstTouchApply(args[1:])
	case "status":
		return cmdFirstTouchStatus(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown first-touch command %q\n", args[0])
		return 2
	}
}

func cmdFirstTouchSeal(args []string) int {
	fs := flag.NewFlagSet("first-touch seal", flag.ContinueOnError)
	orgRaw := fs.String("org-id", "", "organization UUID")
	path := fs.String("manifest", "", "agent-authored research manifest path")
	outPath := fs.String("out", "", "new protected sealed manifest path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	orgID, err := uuid.Parse(strings.TrimSpace(*orgRaw))
	if err != nil || strings.TrimSpace(*path) == "" || strings.TrimSpace(*outPath) == "" {
		fmt.Fprintln(os.Stderr, "--org-id, --manifest, and --out are required")
		return 2
	}
	if strings.TrimSpace(*path) == strings.TrimSpace(*outPath) {
		fmt.Fprintln(os.Stderr, "--out must differ from --manifest")
		return 2
	}
	raw, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "first-touch research manifest: %v\n", err)
		return 1
	}
	var manifest confenge.DelegatedFirstTouchManifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		fmt.Fprintf(os.Stderr, "first-touch research manifest: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pool, _, _, err := openFirstTouchService(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "first-touch seal: %v\n", err)
		return 1
	}
	defer pool.Close()
	repo := repository.NewOutreachRepository(pool)
	for i := range manifest.Entries {
		cand, getErr := repo.GetCandidate(ctx, orgID, manifest.Entries[i].ContactCandidateID)
		if getErr != nil {
			fmt.Fprintf(os.Stderr, "first-touch seal: entry %d candidate lookup failed\n", i)
			return 1
		}
		sealed, sealErr := confenge.SealDelegatedFirstTouchEntry(manifest.Entries[i], cand)
		if sealErr != nil {
			fmt.Fprintf(os.Stderr, "first-touch seal: entry %d: %v\n", i, sealErr)
			return 1
		}
		manifest.Entries[i] = sealed
	}
	out, err := os.OpenFile(*outPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "first-touch seal: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	writeErr := enc.Encode(manifest)
	closeErr := out.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(*outPath)
		fmt.Fprintln(os.Stderr, "first-touch seal: protected manifest write failed")
		return 1
	}
	printJSON(map[string]any{"status": "SEALED", "batch_id": manifest.BatchID, "entries": len(manifest.Entries)})
	return 0
}

func openFirstTouchService(ctx context.Context) (*pgxpool.Pool, confenge.Service, confenge.Config, error) {
	maybeLoadDotEnv()
	cfg := confenge.LoadConfig()
	if !cfg.Enabled {
		return nil, nil, cfg, fmt.Errorf("CONFENGE_OUTREACH_ENABLED is false")
	}
	if !cfg.DelegatedFirstTouchEnabled {
		return nil, nil, cfg, fmt.Errorf("%s is false", confenge.EnvDelegatedFirstTouch)
	}
	if err := cfg.ValidateStartup(cfg.AppEnv); err != nil {
		return nil, nil, cfg, err
	}
	pool, err := pgxpool.New(ctx, primaryDSN())
	if err != nil {
		return nil, nil, cfg, err
	}
	repo := repository.NewOutreachRepository(pool)
	svc := confenge.NewService(cfg, repo, nil)
	svc.WireDispatch(pool)
	svc.WirePolicyAuth(repository.NewConfengePolicyRepository(pool))
	svc.WireDelegatedFirstTouch(pool)
	svc.WireOrgRisk(orgrisk.NewService(repository.NewOrgRiskRepository(pool), nil))
	return pool, svc, cfg, nil
}

func cmdFirstTouchAuthorizePolicy(args []string) int {
	fs := flag.NewFlagSet("first-touch authorize-policy", flag.ContinueOnError)
	orgRaw := fs.String("org-id", "", "organization UUID")
	actorRaw := fs.String("actor", "", "founder user UUID")
	sender := fs.String("sender", "", "existing sender mailbox")
	maxRate := fs.Int("max-rate-per-hour", 10, "policy cap per hour")
	confirm := fs.String("confirm", "", "must equal CFG-FIRST-TOUCH-ROUTING-v1")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	orgID, orgErr := uuid.Parse(strings.TrimSpace(*orgRaw))
	actorID, actorErr := uuid.Parse(strings.TrimSpace(*actorRaw))
	if orgErr != nil || actorErr != nil || strings.TrimSpace(*sender) == "" {
		fmt.Fprintln(os.Stderr, "--org-id, --actor, and --sender are required")
		return 2
	}
	if *confirm != confenge.DelegatedFirstTouchPolicyV1 {
		fmt.Fprintln(os.Stderr, "authorization requires --confirm CFG-FIRST-TOUCH-ROUTING-v1")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, svc, _, err := openFirstTouchService(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "first-touch policy: %v\n", err)
		return 1
	}
	defer pool.Close()
	settings, err := repository.NewOutreachRepository(pool).GetOrgSettings(ctx, orgID)
	if err != nil || settings == nil || settings.CampaignID == nil {
		fmt.Fprintln(os.Stderr, "first-touch policy: canonical CONFENGE campaign is not bootstrapped")
		return 1
	}
	if active, xerr := svc.GetActiveCampaignPolicy(ctx, orgID, *settings.CampaignID); xerr != nil {
		fmt.Fprintf(os.Stderr, "first-touch policy: %s\n", xerr.Message)
		return 1
	} else if active != nil {
		if active.PromptPolicyVersion == confenge.DelegatedFirstTouchPolicyV1 && active.AuthorizedByLabel == confenge.DelegatedFirstTouchAuthority {
			printJSON(map[string]any{"status": "ACTIVE", "policy_authorization_id": active.ID, "policy_version": active.PromptPolicyVersion, "max_rate_per_hour": active.MaxRatePerHour})
			return 0
		}
		fmt.Fprintln(os.Stderr, "first-touch policy: a different active campaign policy must be explicitly revoked first")
		return 1
	}
	auth, xerr := svc.AuthorizeCampaignPolicy(ctx, orgID, actorID, &models.CampaignPolicyAuthorization{
		CampaignID:               *settings.CampaignID,
		PromptPolicyVersion:      confenge.DelegatedFirstTouchPolicyV1,
		ValidatorVersion:         confenge.DelegatedFirstTouchValidatorV1,
		ContactPolicyVersion:     confenge.DelegatedFirstTouchContactPolicyV1,
		TemplatePolicyVersion:    confenge.DelegatedFirstTouchTemplateV1,
		SenderMailbox:            strings.ToLower(strings.TrimSpace(*sender)),
		Channel:                  models.OutreachChannelEmail,
		AllowedRiskClass:         "GREEN",
		MaxRatePerHour:           *maxRate,
		AllowPolicyTemplateGREEN: true,
		EffectiveAt:              time.Now().UTC(),
		AuthorizedByLabel:        confenge.DelegatedFirstTouchAuthority,
	})
	if xerr != nil {
		fmt.Fprintf(os.Stderr, "first-touch policy: %s\n", xerr.Message)
		return 1
	}
	printJSON(map[string]any{"status": "AUTHORIZED", "policy_authorization_id": auth.ID, "policy_version": auth.PromptPolicyVersion, "authority": auth.AuthorizedByLabel, "max_rate_per_hour": auth.MaxRatePerHour})
	return 0
}

func cmdFirstTouchApply(args []string) int {
	fs := flag.NewFlagSet("first-touch apply", flag.ContinueOnError)
	orgRaw := fs.String("org-id", "", "organization UUID")
	path := fs.String("manifest", "", "agent-authored manifest path")
	dryRun := fs.Bool("dry-run", false, "validate without writes")
	confirm := fs.String("confirm", "", "must equal CFG-FIRST-TOUCH-ROUTING-v1 for writes")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	orgID, err := uuid.Parse(strings.TrimSpace(*orgRaw))
	if err != nil || strings.TrimSpace(*path) == "" {
		fmt.Fprintln(os.Stderr, "--org-id and --manifest are required")
		return 2
	}
	if !*dryRun && *confirm != confenge.DelegatedFirstTouchPolicyV1 {
		fmt.Fprintln(os.Stderr, "writes require --confirm CFG-FIRST-TOUCH-ROUTING-v1")
		return 2
	}
	raw, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "first-touch manifest: %v\n", err)
		return 1
	}
	var manifest confenge.DelegatedFirstTouchManifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		fmt.Fprintf(os.Stderr, "first-touch manifest: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	pool, svc, _, err := openFirstTouchService(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "first-touch apply: %v\n", err)
		return 1
	}
	defer pool.Close()
	report, xerr := svc.ApplyDelegatedFirstTouchManifest(ctx, orgID, manifest, *dryRun)
	if xerr != nil {
		fmt.Fprintf(os.Stderr, "first-touch apply: %s\n", xerr.Message)
		return 1
	}
	printJSON(report)
	if report.ApprovedNotScheduled > 0 {
		return 3
	}
	if report.Held > 0 {
		return 4
	}
	return 0
}

func cmdFirstTouchStatus(args []string) int {
	fs := flag.NewFlagSet("first-touch status", flag.ContinueOnError)
	orgRaw := fs.String("org-id", "", "organization UUID")
	batchID := fs.String("batch-id", "", "optional batch id")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	orgID, err := uuid.Parse(strings.TrimSpace(*orgRaw))
	if err != nil {
		fmt.Fprintln(os.Stderr, "--org-id is required")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, svc, _, err := openFirstTouchService(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "first-touch status: %v\n", err)
		return 1
	}
	defer pool.Close()
	status, xerr := svc.DelegatedFirstTouchStatus(ctx, orgID, strings.TrimSpace(*batchID))
	if xerr != nil {
		fmt.Fprintf(os.Stderr, "first-touch status: %s\n", xerr.Message)
		return 1
	}
	printJSON(status)
	if status.DuplicateLiveAccount > 0 || status.DuplicateLiveRoot > 0 || status.QueuedReadback != status.Counts["QUEUED"] {
		return 3
	}
	return 0
}

func printJSON(value any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(value)
}
