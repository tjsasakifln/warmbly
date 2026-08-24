package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type editorialIssueRow struct {
	ID              uuid.UUID `json:"id"`
	DefectSignature string    `json:"defect_signature"`
	Repository      string    `json:"repository"`
	Title           string    `json:"title"`
	BodyRedacted    string    `json:"body_redacted"`
	Labels          []string  `json:"labels"`
	Attempts        int       `json:"attempts"`
}

func cmdEditorial(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: confenge editorial issues|guidelines COMMAND [flags]")
		return 2
	}
	switch args[0] {
	case "issues":
		return cmdEditorialIssues(args[1:])
	case "guidelines":
		return cmdEditorialGuidelines(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown editorial area %q\n", args[0])
		return 2
	}
}

func editorialPool(ctx context.Context) (*pgxpool.Pool, error) {
	maybeLoadDotEnv()
	return pgxpool.New(ctx, primaryDSN())
}

func cmdEditorialIssues(args []string) int {
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("editorial issues list", flag.ContinueOnError)
		limit := fs.Int("limit", 50, "maximum rows")
		if fs.Parse(args[1:]) != nil {
			return 2
		}
		return listEditorialIssues(*limit)
	case "sync":
		fs := flag.NewFlagSet("editorial issues sync", flag.ContinueOnError)
		limit := fs.Int("limit", 20, "maximum issues to publish")
		confirm := fs.Bool("confirm", false, "create GitHub issues")
		if fs.Parse(args[1:]) != nil {
			return 2
		}
		if !*confirm {
			fmt.Fprintln(os.Stderr, "refusing GitHub writes without --confirm")
			return 2
		}
		return syncEditorialIssues(*limit)
	default:
		fmt.Fprintln(os.Stderr, "usage: confenge editorial issues list|sync [flags]")
		return 2
	}
}

func pendingEditorialIssues(ctx context.Context, pool *pgxpool.Pool, limit int) ([]editorialIssueRow, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := pool.Query(ctx, `
		SELECT id,defect_signature,repository,title,body_redacted,labels,attempts
		FROM confenge_editorial_issue_outbox
		WHERE status IN ('PENDING','FAILED') AND next_attempt_at <= now()
		ORDER BY created_at,id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]editorialIssueRow, 0, limit)
	for rows.Next() {
		var row editorialIssueRow
		var labels []byte
		if err := rows.Scan(&row.ID, &row.DefectSignature, &row.Repository, &row.Title, &row.BodyRedacted, &labels, &row.Attempts); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(labels, &row.Labels)
		out = append(out, row)
	}
	return out, rows.Err()
}

func listEditorialIssues(limit int) int {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := editorialPool(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db: %v\n", err)
		return 1
	}
	defer pool.Close()
	rows, err := pendingEditorialIssues(ctx, pool, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list editorial issues: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rows); err != nil {
		return 1
	}
	return 0
}

func syncEditorialIssues(limit int) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pool, err := editorialPool(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db: %v\n", err)
		return 1
	}
	defer pool.Close()
	rows, err := pendingEditorialIssues(ctx, pool, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load issue outbox: %v\n", err)
		return 1
	}
	failed := false
	for _, row := range rows {
		cmdArgs := []string{"issue", "create", "--repo", row.Repository, "--title", row.Title, "--body", row.BodyRedacted}
		for _, label := range row.Labels {
			if strings.TrimSpace(label) != "" {
				cmdArgs = append(cmdArgs, "--label", label)
			}
		}
		out, cmdErr := exec.CommandContext(ctx, "gh", cmdArgs...).CombinedOutput()
		if cmdErr != nil {
			failed = true
			_, _ = pool.Exec(ctx, `
				UPDATE confenge_editorial_issue_outbox
				SET status='FAILED',attempts=attempts+1,last_error=LEFT($2,1000),
					next_attempt_at=now()+interval '6 hours',updated_at=now()
				WHERE id=$1`, row.ID, strings.TrimSpace(string(out)))
			fmt.Fprintf(os.Stderr, "issue %s failed: %v\n", row.DefectSignature, cmdErr)
			continue
		}
		issueURL := strings.TrimSpace(string(out))
		parts := strings.Split(strings.TrimRight(issueURL, "/"), "/")
		issueNumber, _ := strconv.ParseInt(parts[len(parts)-1], 10, 64)
		_, err = pool.Exec(ctx, `
			UPDATE confenge_editorial_issue_outbox
			SET status='PUBLISHED',github_issue_number=$2,github_issue_url=$3,
				attempts=attempts+1,last_error='',updated_at=now()
			WHERE id=$1`, row.ID, issueNumber, issueURL)
		if err != nil {
			failed = true
			fmt.Fprintf(os.Stderr, "persist published issue %s: %v\n", issueURL, err)
			continue
		}
		fmt.Println(issueURL)
	}
	if failed {
		return 1
	}
	return 0
}

func cmdEditorialGuidelines(args []string) int {
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("editorial guidelines list", flag.ContinueOnError)
		orgRaw := fs.String("org-id", "", "organization UUID")
		if fs.Parse(args[1:]) != nil {
			return 2
		}
		return listEditorialGuidelines(*orgRaw)
	case "propose":
		fs := flag.NewFlagSet("editorial guidelines propose", flag.ContinueOnError)
		orgRaw := fs.String("org-id", "", "organization UUID")
		actorRaw := fs.String("actor", "", "proposing Warmbly user UUID")
		version := fs.String("version", "", "immutable guideline version")
		rulesPath := fs.String("rules", "", "JSON array of structured rules")
		if fs.Parse(args[1:]) != nil {
			return 2
		}
		return proposeEditorialGuidelines(*orgRaw, *actorRaw, *version, *rulesPath)
	case "activate":
		fs := flag.NewFlagSet("editorial guidelines activate", flag.ContinueOnError)
		orgRaw := fs.String("org-id", "", "organization UUID")
		actorRaw := fs.String("actor", "", "reviewing founder UUID")
		idRaw := fs.String("id", "", "guideline set UUID")
		confirm := fs.Bool("confirm", false, "activate this version")
		if fs.Parse(args[1:]) != nil {
			return 2
		}
		if !*confirm {
			fmt.Fprintln(os.Stderr, "refusing activation without --confirm")
			return 2
		}
		return activateEditorialGuidelines(*orgRaw, *actorRaw, *idRaw)
	default:
		fmt.Fprintln(os.Stderr, "usage: confenge editorial guidelines list|propose|activate [flags]")
		return 2
	}
}

func editorialUUID(raw, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%s must be a UUID", name)
	}
	return id, nil
}

func listEditorialGuidelines(orgRaw string) int {
	orgID, err := editorialUUID(orgRaw, "org-id")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := editorialPool(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db: %v\n", err)
		return 1
	}
	defer pool.Close()
	rows, err := pool.Query(ctx, `
		SELECT id,version,status,rules_json,source_signal_ids,reviewed_at,created_at
		FROM confenge_editorial_guideline_sets
		WHERE organization_id=$1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list guidelines: %v\n", err)
		return 1
	}
	defer rows.Close()
	values := make([]map[string]any, 0)
	for rows.Next() {
		var id uuid.UUID
		var version, status string
		var rules, signals []byte
		var reviewedAt *time.Time
		var createdAt time.Time
		if err := rows.Scan(&id, &version, &status, &rules, &signals, &reviewedAt, &createdAt); err != nil {
			return 1
		}
		values = append(values, map[string]any{"id": id, "version": version, "status": status, "rules": json.RawMessage(rules), "source_signal_ids": json.RawMessage(signals), "reviewed_at": reviewedAt, "created_at": createdAt})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(values)
	return 0
}

func proposeEditorialGuidelines(orgRaw, actorRaw, version, rulesPath string) int {
	orgID, err := editorialUUID(orgRaw, "org-id")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	actorID, err := editorialUUID(actorRaw, "actor")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	version = strings.TrimSpace(version)
	if version == "" || strings.TrimSpace(rulesPath) == "" {
		fmt.Fprintln(os.Stderr, "--version and --rules are required")
		return 2
	}
	rules, err := os.ReadFile(rulesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rules: %v\n", err)
		return 1
	}
	var parsed []map[string]any
	if json.Unmarshal(rules, &parsed) != nil || len(parsed) == 0 {
		fmt.Fprintln(os.Stderr, "rules must be a non-empty JSON array of objects")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := editorialPool(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db: %v\n", err)
		return 1
	}
	defer pool.Close()
	var id uuid.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO confenge_editorial_guideline_sets
			(organization_id,version,status,rules_json,proposed_by)
		VALUES ($1,$2,'PROPOSED',$3,$4) RETURNING id`, orgID, version, rules, actorID).Scan(&id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "propose guidelines: %v\n", err)
		return 1
	}
	fmt.Println(id)
	return 0
}

func activateEditorialGuidelines(orgRaw, actorRaw, idRaw string) int {
	orgID, err := editorialUUID(orgRaw, "org-id")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	actorID, err := editorialUUID(actorRaw, "actor")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	id, err := editorialUUID(idRaw, "id")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := editorialPool(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db: %v\n", err)
		return 1
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 1
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `
		UPDATE confenge_editorial_guideline_sets
		SET status='SUPERSEDED',updated_at=now()
		WHERE organization_id=$1 AND status='ACTIVE' AND id<>$2`, orgID, id); err != nil {
		return 1
	}
	tag, err := tx.Exec(ctx, `
		UPDATE confenge_editorial_guideline_sets
		SET status='ACTIVE',reviewed_by=$3,reviewed_at=now(),updated_at=now()
		WHERE organization_id=$1 AND id=$2 AND status='PROPOSED'`, orgID, id, actorID)
	if err != nil || tag.RowsAffected() != 1 {
		fmt.Fprintln(os.Stderr, "guideline set is not a proposed version in this organization")
		return 1
	}
	if err := tx.Commit(ctx); err != nil {
		return 1
	}
	fmt.Println(id)
	return 0
}
