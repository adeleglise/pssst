// Package status holds the declared-status contract every adapter implements,
// and the small helpers they share so that one rule is written once.
package status

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// StatusProvider returns a complete current snapshot, or an error with no partial update.
type StatusProvider interface {
	Fetch(context.Context) (Snapshot, error)
}

// Overall is the reserved component alias carrying the page-level state.
const Overall = "overall"

// Severities is the documented, bounded severity enum. Every one of them is
// exported for every configured source, so a zero is a real zero.
var Severities = [...]string{"none", "minor", "major", "critical", "unknown"}

// SafeID matches a remote identifier that may reach a log field. Anything else
// is remote text: the incident is counted, its identifier dropped.
var SafeID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

type Incident struct {
	ID       string
	State    string
	Severity string
}

type Snapshot struct {
	Components           map[string]bool
	Incidents            map[string]int
	MaintenanceActive    int
	MaintenanceScheduled int
	NextMaintenance      time.Time
	Details              []Incident
}

// NewSnapshot returns an empty snapshot whose overall state is the given one.
func NewSnapshot(overall bool) Snapshot {
	return Snapshot{Components: map[string]bool{Overall: overall}, Incidents: map[string]int{}}
}

// AddIncident counts one unresolved incident. Its detail is kept only when the
// identifier is already a safe token.
func (s *Snapshot) AddIncident(id, state, severity string) {
	s.Incidents[severity]++
	if SafeID.MatchString(id) {
		s.Details = append(s.Details, Incident{ID: id, State: state, Severity: severity})
	}
}

// AddScheduledMaintenance counts one announced window. A zero start still
// counts, but only a dated window can become the next start.
func (s *Snapshot) AddScheduledMaintenance(start time.Time) {
	s.MaintenanceScheduled++
	if !start.IsZero() && (s.NextMaintenance.IsZero() || start.Before(s.NextMaintenance)) {
		s.NextMaintenance = start
	}
}

// ResolveComponents applies the two component rules every adapter shares. The
// overall state is false as soon as any published component is not
// operational, whatever the page rollup claims. Each configured alias must
// resolve to a published component, or the whole snapshot fails: a component
// that disappeared is unknown, never healthy.
func (s *Snapshot) ResolveComponents(states, mappings map[string]string, operational func(string) bool) error {
	for _, state := range states {
		if !operational(state) {
			s.Components[Overall] = false
		}
	}
	for alias, remoteID := range mappings {
		if alias == "" || alias == Overall || remoteID == "" {
			return errors.New("invalid configured component")
		}
		state, found := states[remoteID]
		if !found {
			return errors.New("configured component missing from status source")
		}
		s.Components[alias] = operational(state)
	}
	return nil
}

// Endpoint joins an HTTP(S) base URL and an API path, dropping any query or
// fragment.
func Endpoint(baseURL, path string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("invalid endpoint")
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// ErrInvalidEndpoint is returned by Fetch when the configured URL is unusable.
var ErrInvalidEndpoint = errors.New("invalid status page endpoint")
