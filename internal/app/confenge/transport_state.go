package confenge

import (
	"context"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// Transport state is resolved from every control that can actually stop a send.
// Before this existed the controls were split across two disagreeing
// projections: Config.SendingAllowed covered the environment flag and the
// kill-switch file, while the dispatch governor covered the environment flag,
// the durable control row and the business window. Only the dispatcher itself
// ANDed all of them, so GET /confenge/status could report sending_allowed=true
// while the queue worker refused every claim — or the reverse.
//
// One rule now governs every reader: the most restrictive source wins, and a
// source that cannot be read is never treated as permissive.
const (
	TransportActive  = "ACTIVE"
	TransportPaused  = "PAUSED"
	TransportUnknown = "UNKNOWN"
)

// Named sources, stable for dashboards and drift assertions.
const (
	TransportSourceEnvironment    = "environment"
	TransportSourceFileKillSwitch = "file_kill_switch"
	TransportSourceDurableControl = "durable_control"
	TransportSourceSendWindow     = "send_window"
)

// TransportSourceState is one control's contribution to the resolved state.
type TransportSourceState struct {
	Source string `json:"source"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// TransportState is the single answer to "may a message leave right now?".
type TransportState struct {
	State    string                 `json:"state"`
	Active   bool                   `json:"active"`
	Sources  []TransportSourceState `json:"sources"`
	Blockers []string               `json:"blockers,omitempty"`
}

// resolve applies "most restrictive wins": ACTIVE only when every source is
// ACTIVE; any PAUSED makes it PAUSED; otherwise any UNKNOWN makes it UNKNOWN.
func resolveTransportState(sources []TransportSourceState) TransportState {
	out := TransportState{State: TransportActive, Sources: sources}
	paused := false
	unknown := false
	for _, s := range sources {
		switch s.State {
		case TransportPaused:
			paused = true
		case TransportUnknown:
			unknown = true
		}
		if s.State != TransportActive {
			blocker := s.Source
			if s.Reason != "" {
				blocker = s.Source + ":" + s.Reason
			}
			out.Blockers = append(out.Blockers, blocker)
		}
	}
	switch {
	case paused:
		out.State = TransportPaused
	case unknown:
		out.State = TransportUnknown
	}
	out.Active = out.State == TransportActive
	sort.Strings(out.Blockers)
	return out
}

// fileKillSwitchState distinguishes a confirmed absence from an unreadable
// control. FileKillSwitchActive collapses both into "active" so transport stays
// fail-closed; the projection must still say which one it saw.
func fileKillSwitchState() TransportSourceState {
	path := KillSwitchPath()
	info, err := os.Stat(path)
	switch {
	case err == nil:
		reason := "kill_switch_file_present"
		if body, readErr := os.ReadFile(path); readErr == nil { //nolint:gosec // operator-owned control file
			if first := strings.TrimSpace(strings.SplitN(string(body), "\n", 2)[0]); first != "" {
				reason = "kill_switch_file:" + first
			}
		}
		_ = info
		return TransportSourceState{Source: TransportSourceFileKillSwitch, State: TransportPaused, Reason: reason}
	case os.IsNotExist(err):
		return TransportSourceState{Source: TransportSourceFileKillSwitch, State: TransportActive}
	default:
		return TransportSourceState{
			Source: TransportSourceFileKillSwitch,
			State:  TransportUnknown,
			Reason: "kill_switch_unreadable",
		}
	}
}

// ConfigTransportSources are the controls resolvable without a datastore.
func (c Config) ConfigTransportSources() []TransportSourceState {
	env := TransportSourceState{Source: TransportSourceEnvironment, State: TransportActive}
	if c.SendingPaused {
		env.State = TransportPaused
		env.Reason = "env_paused"
	}
	return []TransportSourceState{env, fileKillSwitchState()}
}

// TransportState resolves only the controls this config can see. Callers with a
// governor must prefer Service.ResolveTransportState so the durable control row
// and the business window participate.
func (c Config) TransportState() TransportState {
	return resolveTransportState(c.ConfigTransportSources())
}

// ResolveTransportState is the authoritative projection. Every reader — the
// dispatch queue worker, the status endpoint, the Control Center — must use it
// so no two of them can disagree about whether transport is live.
func (s *service) ResolveTransportState(ctx context.Context, orgID *uuid.UUID) TransportState {
	sources := s.cfg.ConfigTransportSources()
	if s.governor == nil {
		sources = append(sources, TransportSourceState{
			Source: TransportSourceDurableControl,
			State:  TransportUnknown,
			Reason: "governor_unavailable",
		})
		return resolveTransportState(sources)
	}
	status, err := s.governor.Status(ctx, orgID)
	if err != nil {
		// An unreadable durable control is not an absent pause.
		sources = append(sources,
			TransportSourceState{
				Source: TransportSourceDurableControl,
				State:  TransportUnknown,
				Reason: "durable_control_unreadable",
			},
			TransportSourceState{
				Source: TransportSourceSendWindow,
				State:  TransportUnknown,
				Reason: "send_window_unresolved",
			},
		)
		return resolveTransportState(sources)
	}
	durable := TransportSourceState{Source: TransportSourceDurableControl, State: TransportActive}
	if status.Paused {
		durable.State = TransportPaused
		durable.Reason = status.PauseReason
		if durable.Reason == "" {
			durable.Reason = "paused"
		}
	}
	window := TransportSourceState{Source: TransportSourceSendWindow, State: TransportActive}
	if !status.InSendWindow {
		window.State = TransportPaused
		window.Reason = "outside_send_window"
	}
	return resolveTransportState(append(sources, durable, window))
}
