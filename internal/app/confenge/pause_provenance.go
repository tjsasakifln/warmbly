package confenge

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/confenge/dispatch"
)

const (
	PauseSourceAPI           = "api"
	PauseSourceKillSwitch    = "kill_switch_file"
	PauseSourceWorkerGuard   = "worker_guard"
	PauseSourceEnvironment   = "environment"
	PauseSourceDurable       = "durable_control"
	PauseSourceConfiguration = "configuration"
)

// PauseProvenance is the additive readback for who paused dispatch, when, and
// through which path. The most restrictive source still wins.
type PauseProvenance struct {
	Paused       bool       `json:"paused"`
	PauseReason  string     `json:"pause_reason,omitempty"`
	PausedBy     *uuid.UUID `json:"paused_by,omitempty"`
	PausedAt     *time.Time `json:"paused_at,omitempty"`
	PauseSource  string     `json:"pause_source,omitempty"`
	PauseSources []string   `json:"pause_sources,omitempty"`
}

type killSwitchFile struct {
	Present    bool
	Unreadable bool
	Source     string
	Reason     string
	PausedBy   *uuid.UUID
	PausedAt   *time.Time
}

func parseKillSwitchContent(body string) killSwitchFile {
	out := killSwitchFile{Present: true, Source: PauseSourceKillSwitch, Reason: "kill_switch_file_present"}
	for i, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			if i == 0 {
				out.Reason = "kill_switch_file:" + line
			}
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		switch key {
		case "source":
			switch val {
			case PauseSourceAPI, PauseSourceKillSwitch, PauseSourceWorkerGuard:
				out.Source = val
			}
		case "reason":
			if val != "" {
				out.Reason = "kill_switch_file:" + val
			}
		case "paused_by":
			if id, err := uuid.Parse(val); err == nil {
				out.PausedBy = &id
			}
		case "paused_at":
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				u := t.UTC()
				out.PausedAt = &u
			}
		}
	}
	return out
}

func readKillSwitchFile() killSwitchFile {
	path := KillSwitchPath()
	body, err := os.ReadFile(path) //nolint:gosec // operator-owned control file
	switch {
	case err == nil:
		return parseKillSwitchContent(string(body))
	case os.IsNotExist(err):
		return killSwitchFile{}
	default:
		return killSwitchFile{Unreadable: true, Source: PauseSourceKillSwitch, Reason: "kill_switch_unreadable"}
	}
}

func killSwitchBody(source, reason string, by *uuid.UUID, at time.Time) []byte {
	var b strings.Builder
	b.WriteString("paused\n")
	b.WriteString("source=" + source + "\n")
	if strings.TrimSpace(reason) != "" {
		b.WriteString("reason=" + strings.TrimSpace(reason) + "\n")
	}
	if by != nil && *by != uuid.Nil {
		b.WriteString("paused_by=" + by.String() + "\n")
	}
	if !at.IsZero() {
		b.WriteString("paused_at=" + at.UTC().Format(time.RFC3339) + "\n")
	}
	return []byte(b.String())
}

// EngageKillSwitchFrom writes a sourced kill-switch file. An independent
// file-only pause is left intact so API resume cannot claim it.
func EngageKillSwitchFrom(source, reason string, by *uuid.UUID, at time.Time) error {
	source = strings.TrimSpace(source)
	if source == "" {
		source = PauseSourceKillSwitch
	}
	existing := readKillSwitchFile()
	if existing.Unreadable {
		return os.ErrPermission
	}
	if existing.Present && existing.Source != "" && existing.Source != source && existing.Source != PauseSourceAPI {
		return nil
	}
	path := KillSwitchPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, killSwitchBody(source, reason, by, at), 0o600)
}

// ReleaseKillSwitchIfSource removes the file only when this source engaged it.
func ReleaseKillSwitchIfSource(source string) error {
	existing := readKillSwitchFile()
	if existing.Unreadable {
		return os.ErrPermission
	}
	if !existing.Present {
		return nil
	}
	if existing.Source != source {
		return nil
	}
	return ReleaseKillSwitch()
}

func pauseSourceRank(source string) int {
	switch source {
	case PauseSourceKillSwitch, PauseSourceWorkerGuard:
		return 0
	case PauseSourceEnvironment, PauseSourceConfiguration:
		return 1
	case PauseSourceAPI:
		return 2
	case PauseSourceDurable:
		return 3
	default:
		return 4
	}
}

func mostRestrictivePauseSource(transport TransportState) string {
	best := ""
	bestRank := 99
	for _, src := range transport.Sources {
		if src.State != TransportPaused && src.State != TransportUnknown {
			continue
		}
		name := src.Source
		if name == TransportSourceFileKillSwitch {
			name = PauseSourceKillSwitch
		}
		if name == TransportSourceDurableControl {
			name = PauseSourceDurable
		}
		if name == TransportSourceEnvironment {
			name = PauseSourceEnvironment
		}
		if r := pauseSourceRank(name); r < bestRank {
			best = name
			bestRank = r
		}
	}
	return best
}

// MergePauseProvenance is the shipped readback. A file-only or worker-only
// pause never inherits a Control Center actor. The most restrictive source
// still wins. An unreadable source is treated as paused.
func MergePauseProvenance(ctrl dispatch.ControlState, file killSwitchFile, envPaused bool, envReason string, workerGuard bool) PauseProvenance {
	var sources []string
	paused := false
	reason := ""
	winner := ""
	winnerRank := 99
	consider := func(source, why string, active bool) {
		if !active {
			return
		}
		paused = true
		sources = appendUnique(sources, source)
		if why != "" && reason == "" {
			reason = why
		}
		if r := pauseSourceRank(source); r < winnerRank {
			winner = source
			winnerRank = r
			if why != "" {
				reason = why
			}
		}
	}
	fileSource := file.Source
	if fileSource == "" {
		fileSource = PauseSourceKillSwitch
	}
	consider(fileSource, file.Reason, file.Present || file.Unreadable)
	consider(PauseSourceWorkerGuard, "worker_guard", workerGuard)
	consider(PauseSourceEnvironment, firstNonEmpty(envReason, "env_paused"), envPaused)
	durableReason := ctrl.PauseReason
	if durableReason == "" {
		durableReason = "paused"
	}
	durableSource := strings.TrimSpace(ctrl.PauseSource)
	if durableSource == "" {
		if ctrl.PausedBy != nil && *ctrl.PausedBy != uuid.Nil {
			durableSource = PauseSourceAPI
		} else {
			durableSource = PauseSourceDurable
		}
	}
	consider(durableSource, durableReason, ctrl.Paused)

	out := PauseProvenance{
		Paused:       paused,
		PauseReason:  reason,
		PauseSource:  winner,
		PauseSources: sources,
	}
	// Control Center actor is visible only when API is actually one of the
	// active sources. File-only/worker-only pauses stay unattributed.
	if ctrl.Paused && durableSource == PauseSourceAPI {
		out.PausedBy = ctrl.PausedBy
		out.PausedAt = ctrl.PausedAt
	}
	if file.Present && file.Source == PauseSourceAPI && out.PausedBy == nil {
		out.PausedBy = file.PausedBy
		out.PausedAt = file.PausedAt
	}
	if file.Unreadable {
		out.Paused = true
		out.PauseSource = PauseSourceKillSwitch
		out.PauseReason = file.Reason
		out.PauseSources = appendUnique(out.PauseSources, PauseSourceKillSwitch)
	}
	return out
}

func applyPauseProvenance(st *dispatch.Status, prov PauseProvenance) {
	if st == nil {
		return
	}
	st.Paused = st.Paused || prov.Paused
	if prov.PauseReason != "" {
		st.PauseReason = prov.PauseReason
	}
	st.PauseSource = prov.PauseSource
	st.PausedBy = prov.PausedBy
	st.PausedAt = prov.PausedAt
	st.PauseSources = append([]string{}, prov.PauseSources...)
	for i := range st.Mailboxes {
		st.Mailboxes[i].PauseSource = prov.PauseSource
	}
}
