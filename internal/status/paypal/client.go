// Package paypal reads the event API behind PayPal's own status page, which
// also serves Braintree. PayPal publishes no component inventory: every event
// is an incident or a maintenance window carrying a state and an environment,
// so a snapshot carries the overall state, incident counts and maintenance.
package paypal

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/adeleglise/pssst/internal/httpclient"
	"github.com/adeleglise/pssst/internal/status"
)

const eventsPath = "/api/v1/events"
const maxEvents = 512

const (
	stateOpen       = "open"
	typeIncident    = "incident"
	typeMaintenance = "maintenance"
	// Only production events describe the service a merchant actually uses.
	environmentProduction = "production"
)

var entityID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// Client retrieves PayPal's current event list.
type Client struct {
	endpoint   string
	endpointOK bool
	http       *httpclient.Client
}

func New(baseURL string, headers map[string]string, timeout time.Duration) *Client {
	endpoint, err := endpoint(baseURL, eventsPath)
	return &Client{
		endpoint:   endpoint,
		endpointOK: err == nil,
		http:       httpclient.New(timeout, headers),
	}
}

func (c *Client) Fetch(ctx context.Context) (status.Snapshot, error) {
	if !c.endpointOK {
		return status.Snapshot{}, errors.New("invalid status page endpoint")
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

	snapshot := status.Snapshot{
		Components: map[string]bool{"overall": true},
		Incidents:  make(map[string]int),
	}
	seen := make(map[string]struct{}, len(events))
	for _, event := range events {
		if strings.ToLower(event.State) != stateOpen {
			continue
		}
		if strings.ToLower(event.Environment) != environmentProduction {
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
	snapshot.Components["overall"] = false
	severity := normalizeSeverity(event.Severity)
	snapshot.Incidents[severity]++

	// A reference is remote text. Only an already safe one reaches a log field.
	if !entityID.MatchString(event.ReferenceID) {
		return nil
	}
	if _, exists := seen[event.ReferenceID]; exists {
		return errors.New("duplicate status summary entity")
	}
	seen[event.ReferenceID] = struct{}{}
	snapshot.Details = append(snapshot.Details, status.Incident{ID: event.ReferenceID, State: "active", Severity: severity})
	return nil
}

// addMaintenance splits an open window by its start: PayPal keeps a window open
// both before and during the work, and only the start date separates the two.
func addMaintenance(snapshot *status.Snapshot, event rawEvent, now time.Time) error {
	if event.StartDate == "" {
		snapshot.MaintenanceScheduled++
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
	snapshot.MaintenanceScheduled++
	if snapshot.NextMaintenance.IsZero() || start.Before(snapshot.NextMaintenance) {
		snapshot.NextMaintenance = start
	}
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
