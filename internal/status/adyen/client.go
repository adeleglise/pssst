// Package adyen reads Adyen's own status API. Adyen publishes no component
// inventory and no machine-readable maintenance list: the only current-state
// document is the list of active incidents, so a snapshot carries the overall
// state and incident counts and nothing else.
package adyen

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

const activeIncidentsPath = "/api/incident-messages/active"
const maxIncidents = 256

var entityID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// Client retrieves Adyen's active incident list.
type Client struct {
	endpoint   string
	endpointOK bool
	http       *httpclient.Client
}

func New(baseURL string, headers map[string]string, timeout time.Duration) *Client {
	endpoint, err := endpoint(baseURL, activeIncidentsPath)
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

	snapshot := status.Snapshot{
		Components: map[string]bool{"overall": len(items) == 0},
		Incidents:  make(map[string]int),
	}
	seen := make(map[string]struct{}, len(items))
	for _, incident := range items {
		severity := normalizeSeverity(incident.Severity)
		snapshot.Incidents[severity]++

		// Adyen labels incidents with free prose. Only an identifier that is
		// already safe may reach a log field; otherwise the incident is counted
		// without any detail rather than carrying remote text.
		id := incident.Sys.ID
		if id == "" {
			id = incident.ID
		}
		if !entityID.MatchString(id) {
			continue
		}
		if _, exists := seen[id]; exists {
			return status.Snapshot{}, errors.New("duplicate status summary entity")
		}
		seen[id] = struct{}{}
		snapshot.Details = append(snapshot.Details, status.Incident{ID: id, State: "active", Severity: severity})
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
