// Package source is the one list of declared-status adapters. Configuration
// validation, the scheduler and pssst-check all read it, so adding an adapter
// is one entry here rather than three switches the compiler cannot check.
package source

import (
	"slices"
	"time"

	"github.com/adeleglise/pssst/internal/status"
	"github.com/adeleglise/pssst/internal/status/adyen"
	"github.com/adeleglise/pssst/internal/status/hipay"
	"github.com/adeleglise/pssst/internal/status/instatus"
	"github.com/adeleglise/pssst/internal/status/kener"
	"github.com/adeleglise/pssst/internal/status/paypal"
	"github.com/adeleglise/pssst/internal/status/statuspage"
)

// None is the explicit absence of a declared source. It has no adapter: the
// cache reports it as unconfigured, never as healthy.
const None = "none"

// Adapter describes one declared-status source type.
type Adapter struct {
	// Components is false for a source that publishes no component inventory.
	// A mapping could never resolve there, so validation rejects one.
	Components bool
	// DisplayNameKeys is true when the source identifies components by their
	// displayed label, which legitimately contains spaces.
	DisplayNameKeys bool
	New             func(baseURL string, headers, components map[string]string, timeout time.Duration) status.StatusProvider
}

var adapters = map[string]Adapter{
	"statuspage_v2": {Components: true, New: func(u string, h, c map[string]string, t time.Duration) status.StatusProvider {
		return statuspage.New(u, h, c, t)
	}},
	"instatus_v1": {Components: true, New: func(u string, h, c map[string]string, t time.Duration) status.StatusProvider {
		return instatus.New(u, h, c, t)
	}},
	"hipay_v1": {Components: true, New: func(u string, h, c map[string]string, t time.Duration) status.StatusProvider {
		return hipay.New(u, h, c, t)
	}},
	"kener_v1": {Components: true, DisplayNameKeys: true, New: func(u string, h, c map[string]string, t time.Duration) status.StatusProvider {
		return kener.New(u, h, c, t)
	}},
	"adyen_v1": {New: func(u string, h, _ map[string]string, t time.Duration) status.StatusProvider {
		return adyen.New(u, h, t)
	}},
	"paypal_v1": {New: func(u string, h, _ map[string]string, t time.Duration) status.StatusProvider {
		return paypal.New(u, h, t)
	}},
}

// Lookup returns the adapter registered under a type name. None is not one.
func Lookup(sourceType string) (Adapter, bool) {
	adapter, found := adapters[sourceType]
	return adapter, found
}

// Types lists the adapter type names, sorted, for messages and usage text.
func Types() []string {
	types := make([]string, 0, len(adapters))
	for name := range adapters {
		types = append(types, name)
	}
	slices.Sort(types)
	return types
}
