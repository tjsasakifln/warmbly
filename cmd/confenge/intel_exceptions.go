package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/app/confenge/intel"
)

func cmdIntelExceptions(args []string) int {
	return runIntelExceptions(args, os.Stdout, os.Stderr)
}

func runIntelExceptions(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintf(stderr, `confenge intel-exceptions — operator queue without a UI

Usage:
  confenge intel-exceptions list [--type CODE] [--lane LANE] [--source SRC] [--severity SEV] [--status ST] [--age-min SEC] [--age-max SEC] [--format json|md] [--fixture] [--org-id UUID]
  confenge intel-exceptions show <id> [--format json|md] [--fixture] [--org-id UUID]
  confenge intel-exceptions resolve <id> --action link|defer|reject|mark_external_evidence_required --actor WHO --reason WHY [--link-identity ID] [--link-lead-id ID] [--link-action-id ID] [--link-account-id ID] [--idempotency-key K] [--fixture] [--org-id UUID]

--fixture uses the labeled SYNTHETIC operator set (not live).
Without --fixture, PRIMARY_DB must be set. Missing env fails closed as BLOCKED.
`)
		return 2
	}
	switch args[0] {
	case "list":
		return runIntelExceptionList(args[1:], stdout, stderr)
	case "show":
		return runIntelExceptionShow(args[1:], stdout, stderr)
	case "resolve":
		return runIntelExceptionResolve(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown intel-exceptions command %q\n", args[0])
		return 2
	}
}

func openIntelQueue(orgID string, fixture bool, stderr io.Writer) (intel.Store, time.Time, int) {
	if fixture {
		st := intel.NewMemoryStore()
		intel.LoadOperatorQueue(st, orgID)
		return st, intel.OperatorQueueNow, 0
	}
	dsn := strings.TrimSpace(os.Getenv("PRIMARY_DB"))
	if dsn == "" {
		fmt.Fprintf(stderr, "BLOCKED: PRIMARY_DB is unset and --fixture was not passed.\n")
		fmt.Fprintf(stderr, "prerequisite: export PRIMARY_DB to the operator Postgres DSN, or pass --fixture for the labeled SYNTHETIC queue.\n")
		fmt.Fprintf(stderr, "next: PRIMARY_DB=postgres://... go run ./cmd/confenge intel-exceptions list --format json\n")
		fmt.Fprintf(stderr, "  or: go run ./cmd/confenge intel-exceptions list --fixture --format json\n")
		return nil, time.Time{}, 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(stderr, "BLOCKED: PRIMARY_DB open failed: %v\n", err)
		fmt.Fprintf(stderr, "prerequisite: reachable Postgres at PRIMARY_DB.\n")
		fmt.Fprintf(stderr, "next: go run ./cmd/confenge intel-exceptions list --format json\n")
		return nil, time.Time{}, 2
	}
	return intel.NewPGStore(pool, orgID), time.Now().UTC(), 0
}

func runIntelExceptionList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("intel-exceptions list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	orgStr := fs.String("org-id", intel.OperatorQueueOrgID, "organization UUID")
	typ := fs.String("type", "", "exception type/code")
	lane := fs.String("lane", "", "route family")
	source := fs.String("source", "", "source")
	severity := fs.String("severity", "", "high|medium|low")
	status := fs.String("status", "", "open|deferred|rejected|linked|external_evidence_required")
	ageMin := fs.Int64("age-min", 0, "minimum age in seconds")
	ageMax := fs.Int64("age-max", 0, "maximum age in seconds")
	format := fs.String("format", "json", "json|md")
	fixture := fs.Bool("fixture", false, "labeled SYNTHETIC operator set")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	st, now, code := openIntelQueue(*orgStr, *fixture, stderr)
	if code != 0 {
		return code
	}
	filter := intel.ExceptionFilter{
		Type: *typ, Lane: *lane, Source: *source, Severity: *severity,
		Status: *status, AgeMin: *ageMin, AgeMax: *ageMax,
	}
	xs, err := intel.ListQueue(st, *orgStr, filter, now)
	if err != nil {
		fmt.Fprintf(stderr, "list: %v\n", err)
		return 1
	}
	return writeIntelQueue(stdout, stderr, *format, map[string]any{
		"data":   xs,
		"filter": filter,
		"count":  len(xs),
		"now":    now.UTC().Format(time.RFC3339),
	}, formatExceptionListMD(xs, filter))
}

func runIntelExceptionShow(args []string, stdout, stderr io.Writer) int {
	id, args := takePositionalID(args)
	fs := flag.NewFlagSet("intel-exceptions show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	orgStr := fs.String("org-id", intel.OperatorQueueOrgID, "organization UUID")
	format := fs.String("format", "json", "json|md")
	fixture := fs.Bool("fixture", false, "labeled SYNTHETIC operator set")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if id == "" && fs.NArg() > 0 {
		id = strings.TrimSpace(fs.Arg(0))
	}
	if id == "" {
		fmt.Fprintln(stderr, "usage: confenge intel-exceptions show <id>")
		return 2
	}
	st, now, code := openIntelQueue(*orgStr, *fixture, stderr)
	if code != 0 {
		return code
	}
	ex, err := intel.GetQueueItem(st, *orgStr, id, now)
	if err != nil {
		fmt.Fprintf(stderr, "show: %v\n", err)
		return 1
	}
	if ex == nil {
		fmt.Fprintln(stderr, "exception not found")
		return 1
	}
	return writeIntelQueue(stdout, stderr, *format, map[string]any{"data": ex}, formatExceptionMD(*ex))
}

func takePositionalID(args []string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return strings.TrimSpace(args[0]), args[1:]
	}
	return "", args
}

func runIntelExceptionResolve(args []string, stdout, stderr io.Writer) int {
	id, args := takePositionalID(args)
	fs := flag.NewFlagSet("intel-exceptions resolve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	orgStr := fs.String("org-id", intel.OperatorQueueOrgID, "organization UUID")
	action := fs.String("action", "", "link|defer|reject|mark_external_evidence_required")
	actor := fs.String("actor", "", "operator identity")
	reason := fs.String("reason", "", "why this action")
	linkIdentity := fs.String("link-identity", "", "existing chain identity")
	linkLead := fs.String("link-lead-id", "", "existing lead_id")
	linkAction := fs.String("link-action-id", "", "existing action_id")
	linkAccount := fs.String("link-account-id", "", "existing account_id")
	idem := fs.String("idempotency-key", "", "replay key")
	format := fs.String("format", "json", "json|md")
	fixture := fs.Bool("fixture", false, "labeled SYNTHETIC operator set")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if id == "" && fs.NArg() > 0 {
		id = strings.TrimSpace(fs.Arg(0))
	}
	if id == "" || strings.TrimSpace(*action) == "" || strings.TrimSpace(*actor) == "" || strings.TrimSpace(*reason) == "" {
		fmt.Fprintln(stderr, "usage: confenge intel-exceptions resolve <id> --action ... --actor ... --reason ...")
		return 2
	}
	st, now, code := openIntelQueue(*orgStr, *fixture, stderr)
	if code != 0 {
		return code
	}
	res, err := intel.Resolve(st, *orgStr, id, intel.ResolveRequest{
		Action:         *action,
		Actor:          *actor,
		Reason:         *reason,
		IdempotencyKey: *idem,
		LinkIdentity:   *linkIdentity,
		LinkLeadID:     *linkLead,
		LinkActionID:   *linkAction,
		LinkAccountID:  *linkAccount,
	}, now)
	if err != nil {
		fmt.Fprintf(stderr, "resolve: %v\n", err)
		return 1
	}
	if res.Refused {
		_ = writeIntelQueue(stdout, stderr, *format, map[string]any{"data": res}, formatResolveMD(res))
		return 1
	}
	return writeIntelQueue(stdout, stderr, *format, map[string]any{"data": res}, formatResolveMD(res))
}

func writeIntelQueue(stdout, stderr io.Writer, format string, payload any, md string) int {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "md", "markdown":
		fmt.Fprint(stdout, md)
		return 0
	default:
		raw, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "json: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
}

func formatExceptionListMD(xs []intel.Exception, filter intel.ExceptionFilter) string {
	var b strings.Builder
	b.WriteString("# Intel exception queue\n\n")
	b.WriteString(fmt.Sprintf("count: %d\n", len(xs)))
	if filter.Type != "" {
		b.WriteString("type: " + filter.Type + "\n")
	}
	if filter.Lane != "" {
		b.WriteString("lane: " + filter.Lane + "\n")
	}
	if filter.Source != "" {
		b.WriteString("source: " + filter.Source + "\n")
	}
	if filter.Severity != "" {
		b.WriteString("severity: " + filter.Severity + "\n")
	}
	b.WriteString("\n")
	for _, ex := range xs {
		b.WriteString(fmt.Sprintf("- %s code=%s lane=%s source=%s severity=%s age_s=%d status=%s next=%q\n",
			ex.ID, ex.Code, ex.Lane, ex.Source, ex.Severity, ex.AgeSeconds, ex.Status, ex.NextAction))
	}
	return b.String()
}

func formatExceptionMD(ex intel.Exception) string {
	var b strings.Builder
	b.WriteString("# Exception " + ex.ID + "\n\n")
	b.WriteString("code: " + ex.Code + "\n")
	b.WriteString("lane: " + ex.Lane + "\n")
	b.WriteString("source: " + ex.Source + "\n")
	b.WriteString("severity: " + ex.Severity + "\n")
	b.WriteString("status: " + ex.Status + "\n")
	b.WriteString("age_seconds: " + strconv.FormatInt(ex.AgeSeconds, 10) + "\n")
	b.WriteString("next_action: " + ex.NextAction + "\n")
	b.WriteString("reason: " + ex.Reason + "\n")
	b.WriteString("\n## Evidence\n")
	for _, ev := range ex.Evidence {
		b.WriteString(fmt.Sprintf("- %s %s=%s\n", ev.Kind, ev.Key, ev.Value))
	}
	b.WriteString("\n## History\n")
	for _, ev := range ex.History {
		b.WriteString(fmt.Sprintf("- %s %s %s %s\n", ev.At.UTC().Format(time.RFC3339), ev.Kind, ev.Action, ev.Reason))
	}
	b.WriteString("\n## Allowed actions\n")
	if len(ex.AllowedActions) == 0 {
		b.WriteString("- remain open nominally (no further legal action, or already resolved)\n")
	}
	for _, a := range ex.AllowedActions {
		b.WriteString("- " + a + "\n")
	}
	return b.String()
}

func formatResolveMD(res intel.ResolveResult) string {
	var b strings.Builder
	b.WriteString("# Resolve\n\n")
	b.WriteString("action: " + res.Action + "\n")
	b.WriteString("actor: " + res.Actor + "\n")
	b.WriteString("reason: " + res.Reason + "\n")
	b.WriteString("replay: " + strconv.FormatBool(res.Replay) + "\n")
	b.WriteString("refused: " + strconv.FormatBool(res.Refused) + "\n")
	b.WriteString(fmt.Sprintf("before: status=%s next=%q\n", res.Before.Status, res.Before.NextAction))
	b.WriteString(fmt.Sprintf("after: status=%s next=%q\n", res.After.Status, res.After.NextAction))
	return b.String()
}
