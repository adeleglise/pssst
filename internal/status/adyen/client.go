// Package adyen reads Adyen's own status API. Adyen publishes no component
// inventory and no machine-readable maintenance list: the only current-state
// document is the list of active incidents, so a snapshot carries the overall
// state and incident counts and nothing else.
package adyen

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/adeleglise/pssst/internal/httpclient"
	"github.com/adeleglise/pssst/internal/status"
)

const activeIncidentsPath = "/api/incident-messages/active"
const maxIncidents = 256

// Client retrieves Adyen's active incident list.
type Client struct {
	endpoint   string
	endpointOK bool
	http       *httpclient.Client
}

func New(baseURL string, headers map[string]string, timeout time.Duration) *Client {
	endpoint, err := status.Endpoint(baseURL, activeIncidentsPath)
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
	return decode(body)
}

type rawResponse struct {
	Collection *struct {
		Items *[]rawIncident `json:"items"`
	} `json:"incidentMessageCollection"`
}

type rawIncident struct {
	Sys struct {
		ID string `json:"id"`
	} `json:"sys"`
	ID       string `json:"id"`
	Severity string `json:"severity"`
}

func decode(body []byte) (status.Snapshot, error) {
	var raw rawResponse
	if err := json.Unmarshal(body, &raw); err != nil || raw.Collection == nil || raw.Collection.Items == nil {
		return status.Snapshot{}, errors.New("invalid status summary")
	}
	items := *raw.Collection.Items
	if len(items) > maxIncidents {
		return status.Snapshot{}, errors.New("status summary has too many entities")
	}

	snapshot := status.NewSnapshot(len(items) == 0)
	seen := make(map[string]struct{}, len(items))
	for _, incident := range items {
		// Adyen labels incidents with free prose. Only an identifier that is
		// already safe reaches a log field; otherwise the incident is counted
		// without any detail rather than carrying remote text.
		id := incident.Sys.ID
		if id == "" {
			id = incident.ID
		}
		if status.SafeID.MatchString(id) {
			if _, exists := seen[id]; exists {
				return status.Snapshot{}, errors.New("duplicate status summary entity")
			}
			seen[id] = struct{}{}
		}
		snapshot.AddIncident(id, "active", normalizeSeverity(incident.Severity))
	}
	return snapshot, nil
}

// normalizeSeverity maps the wording Adyen shows on its page onto the
// documented enum. Anything unrecognized stays unknown and still counts.
func normalizeSeverity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "operational":
		return "none"
	case "degraded performance":
		return "minor"
	case "severely degraded performance":
		return "major"
	case "service unavailable", "outage":
		return "critical"
	default:
		return "unknown"
	}
}
