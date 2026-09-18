// Package instatus implements the Instatus status-page adapter. Instatus splits
// current state across two documents, so one snapshot needs both and fails as a
// whole when either is unusable.
package instatus

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gitea.doudnas.home.al1.io/alain/pssst/internal/httpclient"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/status"
)

const (
	summaryPath    = "/summary.json"
	componentsPath = "/v2/components.json"
	maxEntities    = 1024
)

const (
	pageOperational       = "UP"
	componentOperational  = "OPERATIONAL"
	incidentResolved      = "RESOLVED"
	maintenanceScheduled  = "NOTSTARTEDYET"
	maintenanceInProgress = "INPROGRESS"
	maintenanceCompleted  = "COMPLETED"
)

var entityID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// Client retrieves one complete current-state snapshot from an Instatus page.
type Client struct {
	summary    string
	components string
	endpointOK bool
	http       *httpclient.Client
	mapping    map[string]string // stable local alias -> remote component ID
}

func New(baseURL string, headers map[string]string, components map[string]string, timeout time.Duration) *Client {
	summary, summaryErr := endpoint(baseURL, summaryPath)
	componentList, componentErr := endpoint(baseURL, componentsPath)

	mapping := make(map[string]string, len(components))
	for alias, remoteID := range components {
		mapping[alias] = remoteID
	}
	return &Client{
		summary:    summary,
		components: componentList,
		endpointOK: summaryErr == nil && componentErr == nil,
		http:       httpclient.New(timeout, headers),
		mapping:    mapping,
	}
}

func (c *Client) Fetch(ctx context.Context) (status.Snapshot, error) {
	if !c.endpointOK {
		return status.Snapshot{}, errors.New("invalid status page endpoint")
	}
	if len(c.mapping) > maxEntities {
		return status.Snapshot{}, errors.New("too many configured components")
	}

	summaryBody, err := c.http.Get(ctx, c.summary)
	if err != nil {
		return status.Snapshot{}, err
	}
	componentsBody, err := c.http.Get(ctx, c.components)
	if err != nil {
		return status.Snapshot{}, err
	}
	return decode(summaryBody, componentsBody, c.mapping)
}

type rawSummary struct {
	Page struct {
		Status *string `json:"status"`
	} `json:"page"`
	ActiveIncidents    []rawIncident    `json:"activeIncidents"`
	ActiveMaintenances []rawMaintenance `json:"activeMaintenances"`
}

type rawComponents struct {
	Components *[]rawComponent `json:"components"`
}

type rawComponent struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type rawIncident struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Impact string `json:"impact"`
}

type rawMaintenance struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Start  string `json:"start"`
}

func decode(summaryBody, componentsBody []byte, mappings map[string]string) (status.Snapshot, error) {
	invalid := func() (status.Snapshot, error) { return status.Snapshot{}, errors.New("invalid status summary") }

	var summary rawSummary
	if err := json.Unmarshal(summaryBody, &summary); err != nil || summary.Page.Status == nil || *summary.Page.Status == "" {
		return invalid()
	}
	var document rawComponents
	if err := json.Unmarshal(componentsBody, &document); err != nil || document.Components == nil {
		return invalid()
	}
	components := *document.Components
	if len(components)+len(summary.ActiveIncidents)+len(summary.ActiveMaintenances) > maxEntities {
		return status.Snapshot{}, errors.New("status summary has too many entities")
	}

	componentStates := make(map[string]string, len(components))
	for _, component := range components {
		if !entityID.MatchString(component.ID) || component.Status == "" {
			return invalid()
		}
		if _, exists := componentStates[component.ID]; exists {
			return status.Snapshot{}, errors.New("duplicate status summary entity")
		}
		componentStates[component.ID] = component.Status
	}

	snapshot := status.Snapshot{
		Components: map[string]bool{"overall": strings.ToUpper(*summary.Page.Status) == pageOperational},
		Incidents:  make(map[string]int),
	}
	// A contradictory page rollup must not hide an explicitly failed component.
	for _, state := range componentStates {
		if !operational(state) {
			snapshot.Components["overall"] = false
		}
	}
	for alias, remoteID := range mappings {
		if alias == "" || alias == "overall" || remoteID == "" {
			return status.Snapshot{}, errors.New("invalid configured component")
		}
		remoteState, found := componentStates[remoteID]
		if !found {
			return status.Snapshot{}, errors.New("configured component missing from status summary")
		}
		snapshot.Components[alias] = operational(remoteState)
	}

	if err := addIncidents(&snapshot, summary.ActiveIncidents); err != nil {
		return status.Snapshot{}, err
	}
	if err := addMaintenance(&snapshot, summary.ActiveMaintenances); err != nil {
		return status.Snapshot{}, err
	}
	return snapshot, nil
}

func addIncidents(snapshot *status.Snapshot, incidents []rawIncident) error {
	seen := make(map[string]struct{}, len(incidents))
	for _, incident := range incidents {
		if !entityID.MatchString(incident.ID) || incident.Status == "" {
			return errors.New("invalid status summary")
		}
		if _, exists := seen[incident.ID]; exists {
			return errors.New("duplicate status summary entity")
		}
		seen[incident.ID] = struct{}{}

		state := normalizeIncidentState(incident.Status)
		if state == "resolved" {
			continue
		}
		severity := normalizeSeverity(incident.Impact)
		snapshot.Incidents[severity]++
		snapshot.Details = append(snapshot.Details, status.Incident{ID: incident.ID, State: state, Severity: severity})
	}
	return nil
}

func addMaintenance(snapshot *status.Snapshot, windows []rawMaintenance) error {
	seen := make(map[string]struct{}, len(windows))
	for _, window := range windows {
		if !entityID.MatchString(window.ID) || window.Status == "" {
			return errors.New("invalid status summary")
		}
		if _, exists := seen[window.ID]; exists {
			return errors.New("duplicate status summary entity")
		}
		seen[window.ID] = struct{}{}

		// A window without a start still counts, but cannot be the next start.
		var start time.Time
		if window.Start != "" {
			parsed, err := time.Parse(time.RFC3339, window.Start)
			if err != nil {
				return errors.New("invalid status summary")
			}
			start = parsed
		}
		switch strings.ToUpper(window.Status) {
		case maintenanceScheduled:
			snapshot.MaintenanceScheduled++
			if !start.IsZero() && (snapshot.NextMaintenance.IsZero() || start.Before(snapshot.NextMaintenance)) {
				snapshot.NextMaintenance = start
			}
		case maintenanceInProgress:
			snapshot.MaintenanceActive++
		case maintenanceCompleted:
			// A completed window can linger briefly in an otherwise current page.
		default:
			return errors.New("invalid status summary")
		}
	}
	return nil
}

func operational(state string) bool {
	return strings.ToUpper(state) == componentOperational
}

// normalizeSeverity maps Instatus impact values onto the documented enum.
// Instatus has no level above MAJOROUTAGE, so critical never originates here.
func normalizeSeverity(value string) string {
	switch strings.ToUpper(value) {
	case "OPERATIONAL", "NONE":
		return "none"
	case "DEGRADEDPERFORMANCE", "MINOR":
		return "minor"
	case "PARTIALOUTAGE", "MAJOROUTAGE", "MAJOR":
		return "major"
	case "CRITICAL":
		return "critical"
	default:
		return "unknown"
	}
}

func normalizeIncidentState(value string) string {
	switch strings.ToUpper(value) {
	case "INVESTIGATING":
		return "investigating"
	case "IDENTIFIED":
		return "identified"
	case "MONITORING":
		return "monitoring"
	case incidentResolved:
		return "resolved"
	default:
		return "unknown"
	}
}

func endpoint(baseURL, path string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("invalid endpoint")
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
