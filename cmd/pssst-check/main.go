// pssst-check resolves one declared-status source and prints the normalized
// snapshot. It is the operator tool for qualifying a PSP before adding it to a
// configuration: it shows exactly what the adapter would export, and nothing
// else contacts the provider.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/adeleglise/pssst/internal/config"
	"github.com/adeleglise/pssst/internal/status"
	"github.com/adeleglise/pssst/internal/status/adyen"
	"github.com/adeleglise/pssst/internal/status/hipay"
	"github.com/adeleglise/pssst/internal/status/instatus"
	"github.com/adeleglise/pssst/internal/status/kener"
	"github.com/adeleglise/pssst/internal/status/paypal"
	"github.com/adeleglise/pssst/internal/status/statuspage"
)

const exitUsage = 2

func main() { os.Exit(run()) }

func run() int {
	sourceType := flag.String("type", config.StatusTypeStatuspageV2, "Status source type: statuspage_v2, instatus_v1, hipay_v1, kener_v1, adyen_v1 or paypal_v1")
	baseURL := flag.String("url", "", "Status page base URL")
	components := flag.String("components", "", "Optional alias=remote_id pairs, comma separated")
	timeout := flag.Duration("timeout", 10*time.Second, "Request timeout")
	flag.Parse()

	if *baseURL == "" {
		fmt.Fprintln(os.Stderr, "-url is required")
		return exitUsage
	}
	mapping, err := parseComponents(*components)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}

	var provider status.StatusProvider
	switch *sourceType {
	case config.StatusTypeStatuspageV2:
		provider = statuspage.New(*baseURL, nil, mapping, *timeout)
	case config.StatusTypeInstatusV1:
		provider = instatus.New(*baseURL, nil, mapping, *timeout)
	case config.StatusTypeHiPayV1:
		provider = hipay.New(*baseURL, nil, mapping, *timeout)
	case config.StatusTypeKenerV1:
		provider = kener.New(*baseURL, nil, mapping, *timeout)
	case config.StatusTypeAdyenV1:
		provider = adyen.New(*baseURL, nil, *timeout)
	case config.StatusTypePayPalV1:
		provider = paypal.New(*baseURL, nil, *timeout)
	default:
		fmt.Fprintf(os.Stderr, "unsupported type %q\n", *sourceType)
		return exitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout*2)
	defer cancel()
	snapshot, err := provider.Fetch(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch failed: %v\n", err)
		return 1
	}

	out, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot render snapshot: %v\n", err)
		return 1
	}
	fmt.Println(string(out))
	return 0
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
