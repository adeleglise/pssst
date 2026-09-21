// Package hipay reads the monitor-list API behind HiPay's status page. The
// page renders client side, so the HTML carries no state; the API it calls does.
// It publishes monitors and their current class, but no incident or maintenance
// list, so those stay at zero rather than being inferred.
package hipay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/adeleglise/pssst/internal/httpclient"
	"github.com/adeleglise/pssst/internal/status"
)

const (
	// maxPages bounds a list that reports its own total: a page claiming an
	// ever-growing total must fail rather than poll forever.
	maxPages     = 10
	maxMonitors  = 512
	statusOK     = "ok"
	classHealthy = "success"
)

// Client retrieves the full monitor list, following pagination.
type Client struct {
	endpoint   string
	endpointOK bool
	http       *httpclient.Client
	mapping    map[string]string // stable local alias -> remote monitor ID
}

// New takes the full monitor-list URL published by the status page, because the
// page key is part of that path and appears nowhere else.
func New(listURL string, headers map[string]string, monitors map[string]string, timeout time.Duration) *Client {
	endpoint, err := normalize(listURL)
	mapping := make(map[string]string, len(monitors))
	for alias, remoteID := range monitors {
		mapping[alias] = remoteID
	}
	return &Client{
		endpoint:   endpoint,
		endpointOK: err == nil,
		http:       httpclient.New(timeout, headers),
		mapping:    mapping,
	}
}

func (c *Client) Fetch(ctx context.Context) (status.Snapshot, error) {
	if !c.endpointOK {
		return status.Snapshot{}, errors.New("invalid status page endpoint")
	}

	states := map[string]string{}
	total := 0
	for page := 1; page <= maxPages; page++ {
		body, err := c.http.Get(ctx, fmt.Sprintf("%s?page=%d", c.endpoint, page))
		if err != nil {
			return status.Snapshot{}, err
		}

		before := len(states)
		pageTotal, err := collect(body, states)
		if err != nil {
			return status.Snapshot{}, err
		}

		// The total must hold across every page; a page silently reporting a
		// different total is a sign the list cannot be trusted.
		if total == 0 {
			total = pageTotal
		} else if pageTotal != total {
			return status.Snapshot{}, errors.New("status summary total changed between pages")
		}

		switch {
		case len(states) > total:
			return status.Snapshot{}, errors.New("status summary exceeded its declared total")
		case len(states) == total:
			return build(states, c.mapping)
		case len(states) == before:
			// A page that adds nothing can never reach the total: fail now
			// instead of retrying up to maxPages for no reason.
			return status.Snapshot{}, errors.New("status summary page added no monitor")
		case len(states) > maxMonitors:
			return status.Snapshot{}, errors.New("status summary has too many entities")
		}
	}
	return status.Snapshot{}, errors.New("monitor list did not terminate")
}

type rawResponse struct {
	Status string        `json:"status"`
	Data   *[]rawMonitor `json:"data"`
	PSP    struct {
		// A pointer distinguishes an absent total from an honest zero.
		TotalMonitors *int `json:"totalMonitors"`
	} `json:"psp"`
}

type rawMonitor struct {
	MonitorID   *int64 `json:"monitorId"`
	StatusClass string `json:"statusClass"`
}

// collect adds one page of monitors to states and returns the announced total.
func collect(body []byte, states map[string]string) (int, error) {
	var raw rawResponse
	if err := json.Unmarshal(body, &raw); err != nil || raw.Data == nil || raw.Status != statusOK {
		return 0, errors.New("invalid status summary")
	}
	// A missing or zero total cannot bound the list: the walk would never
	// know when it is complete.
	if raw.PSP.TotalMonitors == nil || *raw.PSP.TotalMonitors <= 0 {
		return 0, errors.New("status summary is missing its monitor total")
	}
	for _, monitor := range *raw.Data {
		if monitor.MonitorID == nil || monitor.StatusClass == "" {
			return 0, errors.New("invalid status summary")
		}
		id := strconv.FormatInt(*monitor.MonitorID, 10)
		if _, exists := states[id]; exists {
			return 0, errors.New("duplicate status summary entity")
		}
		states[id] = monitor.StatusClass
	}
	return *raw.PSP.TotalMonitors, nil
}

func build(states map[string]string, mappings map[string]string) (status.Snapshot, error) {
	snapshot := status.Snapshot{
		Components: map[string]bool{"overall": true},
		Incidents:  make(map[string]int),
	}
	// HiPay publishes no page-level rollup, so the monitors are the rollup.
	for _, class := range states {
		if !operational(class) {
			snapshot.Components["overall"] = false
		}
	}
	for alias, remoteID := range mappings {
		if alias == "" || alias == "overall" || remoteID == "" {
			return status.Snapshot{}, errors.New("invalid configured component")
		}
		class, found := states[remoteID]
		if !found {
			return status.Snapshot{}, errors.New("configured component missing from status summary")
		}
		snapshot.Components[alias] = operational(class)
	}
	return snapshot, nil
}

// operational treats only the documented healthy class as up. An unrecognized
// class is never reported operational.
func operational(class string) bool {
	return strings.ToLower(strings.TrimSpace(class)) == classHealthy
}

func normalize(listURL string) (string, error) {
	u, err := url.Parse(listURL)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("invalid endpoint")
	}
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}
