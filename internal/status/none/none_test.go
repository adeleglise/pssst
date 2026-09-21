package none_test

import (
	"context"
	"testing"

	"github.com/adeleglise/pssst/internal/status/none"
)

func TestProviderReturnsAnEmptyUnconfiguredSnapshot(t *testing.T) {
	t.Parallel()

	snapshot, err := (none.Provider{}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(snapshot.Components) != 0 || len(snapshot.Incidents) != 0 || len(snapshot.Details) != 0 || snapshot.MaintenanceActive != 0 || snapshot.MaintenanceScheduled != 0 || !snapshot.NextMaintenance.IsZero() {
		t.Errorf("snapshot = %#v, want empty unconfigured snapshot", snapshot)
	}
}
