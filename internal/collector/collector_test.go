package collector

import (
	"strings"
	"testing"
	"time"

	"gitea.doudnas.home.al1.io/alain/pssst/internal/blackbox"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/cache"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/config"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/status"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestCollectorSeparatesUnknownAndFailedSignals(t *testing.T) {
	c := cache.New(config.Config{PSPs: []config.PSP{{ID: "demo", Status: config.Status{Type: "statuspage_v2"}, Probes: []config.Probe{{ID: "api"}}}, {ID: "none", Status: config.Status{Type: "none"}}}})
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(New(c, "test"))
	check := func(want string, names ...string) {
		t.Helper()
		if err := testutil.GatherAndCompare(reg, strings.NewReader(want), names...); err != nil {
			t.Fatal(err)
		}
	}
	check(`# HELP psp_status_source_configured Whether an official status source is configured.
# TYPE psp_status_source_configured gauge
psp_status_source_configured{psp="demo"} 1
psp_status_source_configured{psp="none"} 0
`, "psp_status_source_configured")
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == "psp_declared_operational" || f.GetName() == "psp_probe_success" {
			t.Fatal("fabricated an observation before first success")
		}
	}
	now := time.Unix(1000, 0)
	if err := c.UpdateStatus("demo", status.Snapshot{Components: map[string]bool{"overall": false}, Incidents: map[string]int{"major": 1}}, true, now); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateProbe("demo", "api", blackbox.Result{Success: true, DurationSeconds: 0.2}, true, now); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateProbe("demo", "api", blackbox.Result{}, false, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	check(`# HELP psp_declared_operational Whether a declared component is operational (last valid snapshot).
# TYPE psp_declared_operational gauge
psp_declared_operational{component="overall",psp="demo"} 0
# HELP psp_probe_collection_up Whether the last attempt collected a valid Blackbox response.
# TYPE psp_probe_collection_up gauge
psp_probe_collection_up{endpoint="api",psp="demo"} 0
# HELP psp_probe_success Whether the last collected probe succeeded.
# TYPE psp_probe_success gauge
psp_probe_success{endpoint="api",psp="demo"} 1
`, "psp_declared_operational", "psp_probe_collection_up", "psp_probe_success")
	families, err = reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		for _, m := range f.Metric {
			for _, l := range m.Label {
				switch l.GetName() {
				case "psp", "endpoint", "component", "severity", "version", "kind":
				default:
					t.Fatalf("unexpected label %s", l.GetName())
				}
			}
		}
	}
}

// psp_info carries the entity class. Its kind label must stay inside the
// documented enum so a config typo cannot invent a new series.
func TestInfoKindStaysBounded(t *testing.T) {
	c := cache.New(config.Config{PSPs: []config.PSP{
		{ID: "demo", Kind: config.KindPSP},
		{ID: "acq", Kind: "acquirer"},
		{ID: "bank", Kind: "bank"},
	}})
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(New(c, "test"))

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range families {
		if f.GetName() != "psp_info" {
			continue
		}
		for _, m := range f.Metric {
			for _, l := range m.Label {
				if l.GetName() == "kind" {
					seen[l.GetValue()] = true
				}
			}
			if m.GetGauge().GetValue() != 1 {
				t.Error("psp_info must always be 1")
			}
		}
	}
	for _, kind := range config.Kinds {
		if !seen[kind] {
			t.Errorf("kind %q missing from psp_info", kind)
		}
	}
	if len(seen) != len(config.Kinds) {
		t.Errorf("psp_info exported %d kinds, want %d", len(seen), len(config.Kinds))
	}
}
