// pssst-check resolves declared-status sources and prints what the adapters
// would export. It is the operator tool for qualifying a PSP before adding it
// to a configuration, and for auditing a whole inventory against the live
// pages: run it with -config and compare each line with the page a human sees.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/adeleglise/pssst/internal/config"
	"github.com/adeleglise/pssst/internal/source"
	"github.com/adeleglise/pssst/internal/status"
)

const exitUsage = 2

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("pssst-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "Check every declared source of this inventory instead of one -url")
	sourceType := flags.String("type", "statuspage_v2", "Status source type: "+strings.Join(source.Types(), ", "))
	baseURL := flags.String("url", "", "Status page base URL")
	components := flags.String("components", "", "Optional alias=remote_id pairs, comma separated")
	timeout := flags.Duration("timeout", 10*time.Second, "Request timeout")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "-timeout must be positive")
		return exitUsage
	}
	if *configPath != "" {
		return checkInventory(*configPath, *timeout, stdout, stderr)
	}

	if *baseURL == "" {
		fmt.Fprintln(stderr, "-url or -config is required")
		return exitUsage
	}
	mapping, err := parseComponents(*components)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	adapter, found := source.Lookup(*sourceType)
	if !found {
		fmt.Fprintf(stderr, "unsupported type %q\n", *sourceType)
		return exitUsage
	}
	if !adapter.Components && len(mapping) > 0 {
		fmt.Fprintf(stderr, "%s publishes no components\n", *sourceType)
		return exitUsage
	}

	snapshot, err := fetch(adapter.New(*baseURL, nil, mapping, *timeout), *timeout)
	if err != nil {
		fmt.Fprintf(stderr, "fetch failed: %v\n", err)
		return 1
	}
	out, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "cannot render snapshot: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, string(out))
	return 0
}

// checkInventory fetches every declared source once, with the inventory's own
// URLs, headers and mappings, and prints one line per provider. A provider
// with type none is listed as absent, never as healthy. Any failed source
// makes the exit status non-zero.
func checkInventory(path string, timeout time.Duration, stdout, stderr io.Writer) int {
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "invalid configuration: %v\n", err)
		return exitUsage
	}
	failed := 0
	fmt.Fprintln(stdout, "PSP\tTYPE\tRESULT\tOVERALL\tCOMPONENTS DOWN\tINCIDENTS\tMAINTENANCE")
	for _, psp := range cfg.PSPs {
		adapter, found := source.Lookup(psp.Status.Type)
		if !found {
			fmt.Fprintf(stdout, "%s\t%s\tabsent\t-\t-\t-\t-\n", psp.ID, psp.Status.Type)
			continue
		}
		snapshot, err := fetch(adapter.New(psp.Status.BaseURL, psp.Status.Headers, psp.Status.Components, timeout), timeout)
		if err != nil {
			failed++
			fmt.Fprintf(stdout, "%s\t%s\terror: %v\t-\t-\t-\t-\n", psp.ID, psp.Status.Type, err)
			continue
		}
		fmt.Fprintf(stdout, "%s\t%s\tok\t%s\t%s\t%s\t%d active, %d scheduled\n",
			psp.ID, psp.Status.Type, state(snapshot.Components[status.Overall]),
			componentsDown(snapshot), incidents(snapshot), snapshot.MaintenanceActive, snapshot.MaintenanceScheduled)
	}
	if failed > 0 {
		fmt.Fprintf(stderr, "%d declared source(s) failed\n", failed)
		return 1
	}
	return 0
}

func fetch(provider status.StatusProvider, timeout time.Duration) (status.Snapshot, error) {
	// Instatus and HiPay make more than one request per snapshot.
	ctx, cancel := context.WithTimeout(context.Background(), timeout*2)
	defer cancel()
	return provider.Fetch(ctx)
}

func state(operational bool) string {
	if operational {
		return "operational"
	}
	return "NOT operational"
}

func componentsDown(snapshot status.Snapshot) string {
	var down []string
	for alias, operational := range snapshot.Components {
		if alias != status.Overall && !operational {
			down = append(down, alias)
		}
	}
	if len(down) == 0 {
		return "none"
	}
	slices.Sort(down)
	return strings.Join(down, ",")
}

func incidents(snapshot status.Snapshot) string {
	var parts []string
	for _, severity := range status.Severities {
		if n := snapshot.Incidents[severity]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d", severity, n))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ",")
}

// parseComponents reads alias=remote_id pairs without touching the file system.
func parseComponents(raw string) (map[string]string, error) {
	if raw == "" {
		return nil, nil
	}
	mapping := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		alias, remoteID, found := strings.Cut(strings.TrimSpace(pair), "=")
		if !found || alias == "" || remoteID == "" {
			return nil, fmt.Errorf("component %q must be alias=remote_id", pair)
		}
		mapping[alias] = remoteID
	}
	return mapping, nil
}
