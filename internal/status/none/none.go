// Package none implements the intentionally unconfigured status-source type.
package none

import (
	"context"

	"github.com/adeleglise/pssst/internal/status"
)

// Provider has no upstream source. Its empty snapshot must be interpreted as
// unconfigured by the runtime, never as a declared operational state.
type Provider struct{}

func (Provider) Fetch(ctx context.Context) (status.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return status.Snapshot{}, err
	}
	return status.Snapshot{Components: map[string]bool{}, Incidents: map[string]int{}}, nil
}
