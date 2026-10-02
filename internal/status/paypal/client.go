// Package paypal reads the event API behind PayPal's own status page, which
// also serves Braintree. PayPal publishes no component inventory: every event
// is an incident or a maintenance window carrying a state and an environment,
// so a snapshot carries the overall state, incident counts and maintenance.
package paypal

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/adeleglise/pssst/internal/httpclient"
	"github.com/adeleglise/pssst/internal/status"
)

const eventsPath = "/api/v1/events"
const maxEvents = 512

const (
	stateOpen       = "open"
	stateClosed     = "closed"
	typeIncident    = "incident"
	typeMaintenance = "maintenance"
	// Only production events describe the service a merchant actually uses.
	environmentProduction = "production"
	environmentSandbox    = "sandbox"
)

// Client retrieves PayPal's current event list.
type Client struct {
	endpoint   string
	endpointOK bool
	http       *httpclient.Client
}

func New(baseURL string, headers map[string]string, timeout time.Duration) *Client {
	endpoint, err := status.Endpoint(baseURL, eventsPath)
	return &Client{
		endpoint:   endpoint,
		endpointOK: err == nil,
		http:       httpclient.New(timeout, headers),
	}
}

func (c *Client) Fetch(ctx context.Context) (status.Snapshot, error) {
	if !c.endpointOK {
		return status.Snapshot{}, status.ErrInvalidEndpoint
	}
	body, err := c.http.Get(ctx, c.endpoint)
	if err != nil {
		return status.Snapshot{}, err
	}
	return decode(body, time.Now())
}

type rawResponse struct {
	Result *[]rawEvent `json:"result"`
}

type rawEvent struct {
	ReferenceID string  `json:"referenceId"`
	State       string  `json:"state"`
	Type        string  `json:"type"`
	Environment string  `json:"environment"`
	Severity    *string `json:"severity"`
	StartDate   string  `json:"startDate"`
}

func decode(body []byte, now time.Time) (status.Snapshot, error) {
	var raw rawResponse
	if err := json.Unmarshal(body, &raw); err != nil || raw.Result == nil {
		return status.Snapshot{}, errors.New("invalid status summary")
	}
	events := *raw.Result
	if len(events) > maxEvents {
		return status.Snapshot{}, errors.New("status summary has too many entities")
	}

	snapshot := status.NewSnapshot(true)
	seen := make(map[string]struct{}, len(events))
	for _, event := range events {
		state := strings.ToLower(event.State)
		environment := strings.ToLower(event.Environment)

		// A closed event is over, and a sandbox event never describes the
		// production service a merchant uses. Either alone drops the event.
		if state == stateClosed || environment == environmentSandbox {
			continue
		}

		// Anything other than an open production event carries a state or an
		// environment the allow-list does not recognize. Count it rather than
		// guess whether it is safe to ignore.
		if state != stateOpen || environment != environmentProduction {
			if err := addIncident(&snapshot, event, seen); err != nil {
				return status.Snapshot{}, err
			}
			continue
		}

		switch strings.ToLower(event.Type) {
		case typeIncident:
			if err := addIncident(&snapshot, event, seen); err != nil {
				return status.Snapshot{}, err
			}
		case typeMaintenance:
			if err := addMaintenance(&snapshot, event, now); err != nil {
				return status.Snapshot{}, err
			}
		default:
			// An unrecognized event type is counted as an incident of unknown
			// severity rather than silently dropped.
			if err := addIncident(&snapshot, event, seen); err != nil {
				return status.Snapshot{}, err
			}
		}
	}
	return snapshot, nil
}

func addIncident(snapshot *status.Snapshot, event rawEvent, seen map[string]struct{}) error {
	snapshot.Components[status.Overall] = false
	// A reference is remote text. Only an already safe one reaches a log field.
	if status.SafeID.MatchString(event.ReferenceID) {
		if _, exists := seen[event.ReferenceID]; exists {
			return errors.New("duplicate status summary entity")
		}
		seen[event.ReferenceID] = struct{}{}
	}
	snapshot.AddIncident(event.ReferenceID, "active", normalizeSeverity(event.Severity))
	return nil
}

// addMaintenance splits an open window by its start: PayPal keeps a window open
// both before and during the work, and only the start date separates the two.
func addMaintenance(snapshot *status.Snapshot, event rawEvent, now time.Time) error {
	if event.StartDate == "" {
		snapshot.AddScheduledMaintenance(time.Time{})
		return nil
	}
	start, err := time.Parse(time.RFC3339, event.StartDate)
	if err != nil {
		return errors.New("invalid status summary")
	}
	if !start.After(now) {
		snapshot.MaintenanceActive++
		return nil
	}
	snapshot.AddScheduledMaintenance(start)
	return nil
}

// normalizeSeverity maps PayPal's wording onto the documented enum. PayPal
// leaves severity null on many events, which stays unknown and still counts.
func normalizeSeverity(value *string) string {
	if value == nil {
		return "unknown"
	}
	switch strings.ToLower(strings.TrimSpace(*value)) {
	case "operational", "none":
		return "none"
	case "degraded performance", "minor":
		return "minor"
	case "partial outage", "major":
		return "major"
	case "service disruption", "outage", "critical":
		return "critical"
	default:
		return "unknown"
	}
}
