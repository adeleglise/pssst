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
				case "psp", "endpoint", "component", "severity", "version", "kind", "outcome":
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

// Rates need cumulative counters, not gauges: a Datadog agent scraping this
// endpoint must be able to compute an error rate over time. The outcome label
// is bounded to two values so the series count stays predictable.
func TestCountersAreCumulativeAndTyped(t *testing.T) {
	cfg := config.Config{PSPs: []config.PSP{{
		ID:     "demo",
		Kind:   config.KindPSP,
		Status: config.Status{Type: config.StatusTypeStatuspageV2, BaseURL: "https://status.example.test"},
		Probes: []config.Probe{{ID: "api", Module: "http_2xx", Target: "https://api.example.test/"}},
	}}}
	c := cache.New(cfg)
	now := time.Unix(1789000000, 0)

	// Two status polls: one good, one failed.
	if err := c.UpdateStatus("demo", status.Snapshot{Components: map[string]bool{"overall": true}}, true, now); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateStatus("demo", status.Snapshot{}, false, now); err != nil {
		t.Fatal(err)
	}
	// Three collections: one failed, one collected a failing probe, one good.
	if err := c.UpdateProbe("demo", "api", blackbox.Result{}, false, now); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateProbe("demo", "api", blackbox.Result{Success: false}, true, now); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateProbe("demo", "api", blackbox.Result{Success: true}, true, now); err != nil {
		t.Fatal(err)
	}

	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(New(c, "test"))
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}

	counters := map[string]map[string]float64{}
	types := map[string]string{}
	for _, f := range families {
		if !strings.HasSuffix(f.GetName(), "_total") {
			continue
		}
		types[f.GetName()] = f.GetType().String()
		counters[f.GetName()] = map[string]float64{}
		for _, m := range f.Metric {
			outcome := ""
			for _, l := range m.Label {
				if l.GetName() == "outcome" {
					outcome = l.GetValue()
				}
			}
			counters[f.GetName()][outcome] = m.GetCounter().GetValue()
		}
	}

	for name, want := range map[string]map[string]float64{
		"psp_status_poll_total":      {"success": 1, "failure": 1},
		"psp_probe_collection_total": {"success": 2, "failure": 1},
		"psp_probe_result_total":     {"success": 1, "failure": 1},
	} {
		if types[name] != "COUNTER" {
			t.Errorf("%s type = %q, want COUNTER", name, types[name])
		}
		for outcome, value := range want {
			if got := counters[name][outcome]; got != value {
				t.Errorf("%s{outcome=%q} = %v, want %v", name, outcome, got, value)
			}
		}
		if len(counters[name]) != 2 {
			t.Errorf("%s has %d outcome values, want exactly 2", name, len(counters[name]))
		}
	}
}
