// Package statuspage implements the Statuspage API v2 summary adapter.
package statuspage

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"time"

	"github.com/adeleglise/pssst/internal/httpclient"
	"github.com/adeleglise/pssst/internal/status"
)

const maxEntities = 1024

// Client retrieves one complete current-state snapshot from Statuspage.
type Client struct {
	endpoint   string
	endpointOK bool
	http       *httpclient.Client
	components map[string]string // stable local alias -> remote component ID
}

func New(baseURL string, headers map[string]string, components map[string]string, timeout time.Duration) *Client {
	endpoint, err := status.Endpoint(baseURL, "/api/v2/summary.json")
	return &Client{
		endpoint:   endpoint,
		endpointOK: err == nil,
		http:       httpclient.New(timeout, headers),
		components: maps.Clone(components),
	}
}

func (c *Client) Fetch(ctx context.Context) (status.Snapshot, error) {
	if !c.endpointOK {
		return status.Snapshot{}, status.ErrInvalidEndpoint
	}
	if len(c.components) > maxEntities {
		return status.Snapshot{}, errors.New("too many configured components")
	}
	body, err := c.http.Get(ctx, c.endpoint)
	if err != nil {
		return status.Snapshot{}, err
	}
	return decodeSummary(body, c.components)
}

type rawSummary struct {
	Components            json.RawMessage `json:"components"`
	Incidents             json.RawMessage `json:"incidents"`
	ScheduledMaintenances json.RawMessage `json:"scheduled_maintenances"`
	Status                json.RawMessage `json:"status"`
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
	ID           string `json:"id"`
	Status       string `json:"status"`
	ScheduledFor string `json:"scheduled_for"`
}

type rawStatus struct {
	Indicator *string `json:"indicator"`
}

func decodeSummary(body []byte, mappings map[string]string) (status.Snapshot, error) {
	invalid := errors.New("invalid status summary")
	var raw rawSummary
	if err := json.Unmarshal(body, &raw); err != nil || missing(raw.Components) || missing(raw.Status) {
		return status.Snapshot{}, invalid
	}

	var components []rawComponent
	var incidents []rawIncident
	var maintenance []rawMaintenance
	var pageStatus rawStatus
	if json.Unmarshal(raw.Components, &components) != nil || decodeList(raw.Incidents, &incidents) != nil || decodeList(raw.ScheduledMaintenances, &maintenance) != nil || json.Unmarshal(raw.Status, &pageStatus) != nil || pageStatus.Indicator == nil || *pageStatus.Indicator == "" {
		return status.Snapshot{}, invalid
	}
	if len(components)+len(incidents)+len(maintenance) > maxEntities {
		return status.Snapshot{}, errors.New("status summary has too many entities")
	}

	componentStates := make(map[string]string, len(components))
	for _, component := range components {
		if !status.SafeID.MatchString(component.ID) || component.Status == "" {
			return status.Snapshot{}, invalid
		}
		if _, exists := componentStates[component.ID]; exists {
			return status.Snapshot{}, errors.New("duplicate status summary entity")
		}
		componentStates[component.ID] = component.Status
	}

	snapshot := status.NewSnapshot(*pageStatus.Indicator == "none")
	if err := snapshot.ResolveComponents(componentStates, mappings, operational); err != nil {
		return status.Snapshot{}, err
	}

	incidentIDs := make(map[string]struct{}, len(incidents))
	for _, incident := range incidents {
		if !status.SafeID.MatchString(incident.ID) || incident.Status == "" || incident.Impact == "" {
			return status.Snapshot{}, invalid
		}
		if _, exists := incidentIDs[incident.ID]; exists {
			return status.Snapshot{}, errors.New("duplicate status summary entity")
		}
		incidentIDs[incident.ID] = struct{}{}
		state := normalizeIncidentState(incident.Status)
		if state == "resolved" {
			continue
		}
		snapshot.AddIncident(incident.ID, state, normalizeSeverity(incident.Impact))
	}

	maintenanceIDs := make(map[string]struct{}, len(maintenance))
	for _, item := range maintenance {
		if !status.SafeID.MatchString(item.ID) || item.Status == "" {
			return status.Snapshot{}, invalid
		}
		if _, exists := maintenanceIDs[item.ID]; exists {
			return status.Snapshot{}, errors.New("duplicate status summary entity")
		}
		maintenanceIDs[item.ID] = struct{}{}
		// A real page can schedule a window with a null date. It still counts,
		// but only a dated window can become the next start.
		var start time.Time
		if item.ScheduledFor != "" {
			parsed, err := time.Parse(time.RFC3339, item.ScheduledFor)
			if err != nil {
				return status.Snapshot{}, invalid
			}
			start = parsed
		}
		switch item.Status {
		case "scheduled":
			snapshot.AddScheduledMaintenance(start)
		case "in_progress", "verifying":
			snapshot.MaintenanceActive++
		case "completed":
			// A completed item can appear briefly in an otherwise current summary.
		default:
			return status.Snapshot{}, invalid
		}
	}

	return snapshot, nil
}

func operational(state string) bool { return state == "operational" }

// decodeList reads an array that Statuspage omits when it is empty. An omitted
// incidents or maintenance key means nothing is active, not a failed retrieval.
// Components and the page indicator stay mandatory and are decoded directly.
func decodeList[T any](raw json.RawMessage, out *[]T) error {
	if missing(raw) {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func missing(value json.RawMessage) bool {
	return len(value) == 0 || string(value) == "null"
}

func normalizeSeverity(value string) string {
	switch strings.ToLower(value) {
	case "none":
		return "none"
	case "minor":
		return "minor"
	case "major":
		return "major"
	case "critical":
		return "critical"
	default:
		return "unknown"
	}
}

func normalizeIncidentState(value string) string {
	switch strings.ToLower(value) {
	case "investigating", "identified", "monitoring", "resolved":
		return strings.ToLower(value)
	case "postmortem":
		// Statuspage publishes a postmortem after resolution.
		return "resolved"
	default:
		return "unknown"
	}
}
