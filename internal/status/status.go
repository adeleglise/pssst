package status

import (
	"context"
	"time"
)

// StatusProvider returns a complete current snapshot, or an error with no partial update.
type StatusProvider interface {
	Fetch(context.Context) (Snapshot, error)
}

var Severities = [...]string{"none", "minor", "major", "critical", "unknown"}

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
