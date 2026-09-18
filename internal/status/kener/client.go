// Package kener reads a Kener status page. Kener is open source and its API
// requires a key, so a public page is only readable as HTML. This adapter
// therefore parses markup, which is a weaker contract than JSON: it matches the
// structure Kener emits, and any departure from it fails the snapshot rather
// than guessing. A redesign upstream takes the source down and shows up as a
// stale declared signal, which is the intended failure mode.
//
// Structure, as served:
//
//	div.monitor                      one block per component
//	  p.overflow-hidden...            the component name
//	  div[data-ts]                    ninety daily history bars, same colours
//	  span.text-api-<state>           the current state
//
// The history bars reuse the colour classes of the current state, so a parser
// that searched the block for any colour would read history as state. Only the
// text-api- prefix carries the current state, and data-ts nodes are skipped.
package kener

import (
	"context"
	"errors"
	"strings"
	"time"

	"golang.org/x/net/html"

	"gitea.doudnas.home.al1.io/alain/pssst/internal/httpclient"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/status"
)

const (
	monitorClass  = "monitor"
	nameClass     = "overflow-hidden"
	statePrefix   = "text-api-"
	stateUp       = "up"
	maxMonitors   = 256
	maxNameLength = 128
)

// Client retrieves one Kener page and reads the current state of each monitor.
type Client struct {
	endpoint   string
	endpointOK bool
	http       *httpclient.Client
	mapping    map[string]string // stable local alias -> monitor name as displayed
}

func New(baseURL string, headers map[string]string, monitors map[string]string, timeout time.Duration) *Client {
	endpoint, err := normalize(baseURL)
	mapping := make(map[string]string, len(monitors))
	for alias, name := range monitors {
		mapping[alias] = name
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
	body, err := c.http.Get(ctx, c.endpoint)
	if err != nil {
		return status.Snapshot{}, err
	}
	return decode(body, c.mapping)
}

func decode(body []byte, mappings map[string]string) (status.Snapshot, error) {
	document, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return status.Snapshot{}, errors.New("invalid status page")
	}

	states := map[string]string{}
	var walk func(*html.Node) error
	walk = func(node *html.Node) error {
		if isMonitorBlock(node) {
			name, state, err := readMonitor(node)
			if err != nil {
				return err
			}
			if _, exists := states[name]; exists {
				return errors.New("duplicate status page entity")
			}
			if len(states) >= maxMonitors {
				return errors.New("status page has too many entities")
			}
			states[name] = state
			// A monitor block never nests another one.
			return nil
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(document); err != nil {
		return status.Snapshot{}, err
	}
	if len(states) == 0 {
		return status.Snapshot{}, errors.New("status page has no monitor")
	}

	snapshot := status.Snapshot{
		Components: map[string]bool{"overall": true},
		Incidents:  make(map[string]int),
	}
	// Kener publishes no page-level rollup, so the monitors are the rollup.
	for _, state := range states {
		if state != stateUp {
			snapshot.Components["overall"] = false
		}
	}
	for alias, name := range mappings {
		if alias == "" || alias == "overall" || name == "" {
			return status.Snapshot{}, errors.New("invalid configured component")
		}
		state, found := states[name]
		if !found {
			return status.Snapshot{}, errors.New("configured component missing from status page")
		}
		snapshot.Components[alias] = state == stateUp
	}
	return snapshot, nil
}

func isMonitorBlock(node *html.Node) bool {
	return node.Type == html.ElementNode && node.Data == "div" && hasClass(node, monitorClass)
}

// readMonitor returns the displayed name and current state of one block. Both
// must be present: a block missing either is a markup change, not a healthy
// component.
func readMonitor(block *html.Node) (string, string, error) {
	var name, state string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			// History bars carry a timestamp and reuse the state colours.
			if attr(node, "data-ts") != "" {
				return
			}
			if node.Data == "p" && name == "" && hasClass(node, nameClass) {
				name = strings.TrimSpace(text(node))
			}
			if state == "" {
				for _, class := range strings.Fields(attr(node, "class")) {
					if suffix, found := strings.CutPrefix(class, statePrefix); found && suffix != "" {
						state = suffix
						break
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(block)

	if name == "" || len(name) > maxNameLength {
		return "", "", errors.New("monitor without a usable name")
	}
	if state == "" {
		return "", "", errors.New("monitor without a current state")
	}
	return name, state, nil
}

func hasClass(node *html.Node, want string) bool {
	for _, class := range strings.Fields(attr(node, "class")) {
		if class == want {
			return true
		}
	}
	return false
}

func attr(node *html.Node, name string) string {
	for _, a := range node.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func text(node *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return b.String()
}

func normalize(baseURL string) (string, error) {
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return "", errors.New("invalid endpoint")
	}
	return strings.TrimRight(baseURL, "/") + "/", nil
}
