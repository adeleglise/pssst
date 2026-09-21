package cache

import (
	"sync"
	"testing"
	"time"

	"github.com/adeleglise/pssst/internal/blackbox"
	"github.com/adeleglise/pssst/internal/config"
	"github.com/adeleglise/pssst/internal/status"
)

func inventory() config.Config {
	return config.Config{Polling: config.Polling{StatusInterval: time.Minute, ProbeInterval: time.Minute, Timeout: time.Second}, PSPs: []config.PSP{{ID: "demo", Status: config.Status{Type: "statuspage_v2"}, Probes: []config.Probe{{ID: "api"}}}, {ID: "none", Status: config.Status{Type: "none"}}}}
}
func TestPreservationAndReadiness(t *testing.T) {
	c := New(inventory())
	if c.Ready() {
		t.Fatal("ready before first polls")
	}
	first := time.Unix(1000, 0)
	later := first.Add(time.Minute)
	data := status.Snapshot{Components: map[string]bool{"overall": true}, Incidents: map[string]int{"major": 0}}
	if err := c.UpdateStatus("demo", data, true, first); err != nil {
		t.Fatal(err)
	}
	code := 200.0
	if err := c.UpdateProbe("demo", "api", blackbox.Result{Success: true, HTTPStatusCode: &code}, true, first); err != nil {
		t.Fatal(err)
	}
	if !c.Ready() {
		t.Fatal("not ready after attempts")
	}
	data.Components["overall"] = false
	code = 503
	if err := c.UpdateStatus("demo", status.Snapshot{}, false, later); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateProbe("demo", "api", blackbox.Result{}, false, later); err != nil {
		t.Fatal(err)
	}
	snap := c.Snapshot()[0]
	if snap.Status.Up || !snap.Status.Data.Components["overall"] || snap.Status.LastSuccess != first || snap.Status.LastPoll != later {
		t.Fatalf("status lost last-known-good: %+v", snap.Status)
	}
	p := snap.Probes["api"]
	if p.Up || !p.Data.Success || *p.Data.HTTPStatusCode != 200 || p.LastSuccess != first || p.LastCollectionSuccess != first {
		t.Fatalf("probe lost last-known-good: %+v", p)
	}
	snap.Status.Data.Components["overall"] = false
	*p.Data.HTTPStatusCode = 401
	if !c.Snapshot()[0].Status.Data.Components["overall"] || *c.Snapshot()[0].Probes["api"].Data.HTTPStatusCode != 200 {
		t.Fatal("snapshot aliases cache")
	}
	if err := c.UpdateProbe("demo", "api", blackbox.Result{Success: false}, true, later); err != nil {
		t.Fatal(err)
	}
	p = c.Snapshot()[0].Probes["api"]
	if !p.Up || p.Data.Success || p.LastSuccess != first || p.LastCollectionSuccess != later {
		t.Fatal("failed probe confused with collection failure")
	}
	c.Stop()
	if c.Ready() {
		t.Fatal("ready during shutdown")
	}
}
func TestUnknownInventoryRejected(t *testing.T) {
	c := New(inventory())
	if c.UpdateStatus("other", status.Snapshot{}, true, time.Now()) == nil {
		t.Fatal("accepted new source")
	}
	if c.UpdateProbe("demo", "other", blackbox.Result{}, true, time.Now()) == nil {
		t.Fatal("accepted new probe")
	}
}
func TestConcurrentSnapshots(t *testing.T) {
	c := New(inventory())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if err := c.UpdateStatus("demo", status.Snapshot{Components: map[string]bool{"overall": true}}, true, time.Now()); err != nil {
					t.Error(err)
				}
				_ = c.Snapshot()
				_ = c.Ready()
			}
		}()
	}
	wg.Wait()
}
