package kener

import (
	"testing"

	"github.com/adeleglise/pssst/internal/status"
)

// FuzzDecode feeds arbitrary markup to the parser. The result is an error with
// no snapshot, or a snapshot that holds the overall state, every configured
// alias, and an overall state that is never healthier than its components.
func FuzzDecode(f *testing.F) {
	f.Add([]byte(pageWith(monitorBlock("Payments API", "up"), monitorBlock("Dashboard", "down"))))
	f.Add([]byte(pageWith(monitorBlock("Payments API", "degraded"))))
	f.Add([]byte(pageWith(monitorBlock("Payments API", "maintenance"))))
	f.Add([]byte(pageWith(monitorBlock("Payments API", "up"), monitorBlock("Payments API", "up"))))
	f.Add([]byte(pageWith()))
	f.Add([]byte(`<div class="monitor"><p class="x">unterminated`))
	f.Add([]byte(""))
	mappings := map[string]string{"api": "Payments API"}
	f.Fuzz(func(t *testing.T, body []byte) {
		snapshot, err := decode(body, mappings)
		if err != nil {
			if snapshot.Components != nil {
				t.Fatalf("an error must come with no snapshot, got %v", snapshot.Components)
			}
			return
		}
		overall, found := snapshot.Components[status.Overall]
		if !found {
			t.Fatal("snapshot without an overall state")
		}
		api, found := snapshot.Components["api"]
		if !found {
			t.Fatal("snapshot without the configured alias")
		}
		if overall && !api {
			t.Fatal("overall is operational while a component is not")
		}
	})
}
