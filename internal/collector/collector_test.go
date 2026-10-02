package collector

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/adeleglise/pssst/internal/blackbox"
	"github.com/adeleglise/pssst/internal/cache"
	"github.com/adeleglise/pssst/internal/config"
	"github.com/adeleglise/pssst/internal/status"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.yaml.in/yaml/v3"
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
		Status: config.Status{Type: "statuspage_v2", BaseURL: "https://status.example.test"},
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

// Freshness is judged per signal with the rules' own test: never succeeded is
// not fresh, a failed poll inside the window is still fresh, and a success
// older than stale_after is not.
func TestFreshnessMirrorsTheRules(t *testing.T) {
	cfg := config.Config{
		Polling: config.Polling{StatusInterval: time.Minute, ProbeInterval: time.Minute, Timeout: 10 * time.Second},
		PSPs: []config.PSP{{
			ID:     "demo",
			Status: config.Status{Type: "statuspage_v2"},
			Probes: []config.Probe{{ID: "api"}},
		}},
	}
	c := cache.New(cfg)
	collector := New(c, "test")
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(collector)
	start := time.Unix(1789000000, 0)
	staleAfter := 3 * (time.Minute + 10*time.Second)

	check := func(at time.Time, status, probe string) {
		t.Helper()
		collector.now = func() time.Time { return at }
		want := `# HELP psp_probe_fresh Whether the last valid Blackbox response is younger than stale_after.
# TYPE psp_probe_fresh gauge
psp_probe_fresh{endpoint="api",psp="demo"} ` + probe + `
# HELP psp_status_source_fresh Whether the last valid status snapshot is younger than stale_after.
# TYPE psp_status_source_fresh gauge
psp_status_source_fresh{psp="demo"} ` + status + `
`
		if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "psp_status_source_fresh", "psp_probe_fresh"); err != nil {
			t.Fatal(err)
		}
	}

	check(start, "0", "0")

	if err := c.UpdateStatus("demo", status.NewSnapshot(true), true, start); err != nil {
		t.Fatal(err)
	}
	// A collected failing probe is a fresh observation of a failure.
	if err := c.UpdateProbe("demo", "api", blackbox.Result{Success: false}, true, start); err != nil {
		t.Fatal(err)
	}
	check(start, "1", "1")

	// Failed attempts do not end freshness; only age does.
	if err := c.UpdateStatus("demo", status.Snapshot{}, false, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateProbe("demo", "api", blackbox.Result{}, false, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	check(start.Add(staleAfter), "1", "1")
	check(start.Add(staleAfter+time.Second), "0", "0")
}

// The Datadog list is explicit to control cost, and the OpenMetrics check
// silently ignores a name it never sees. Every listed name must be a metric
// this exporter defines, a counter without its _total suffix.
func TestDatadogListNamesRealMetrics(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/kubernetes/deployment.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var deployment struct {
		Spec struct {
			Template struct {
				Metadata struct {
					Annotations map[string]string `yaml:"annotations"`
				} `yaml:"metadata"`
			} `yaml:"template"`
		} `yaml:"spec"`
	}
	// The file holds several documents; the Deployment is the first.
	if err := yaml.NewDecoder(bytes.NewReader(raw)).Decode(&deployment); err != nil {
		t.Fatal(err)
	}
	var check struct {
		OpenMetrics struct {
			Instances []struct {
				Metrics []string `json:"metrics"`
			} `json:"instances"`
		} `json:"openmetrics"`
	}
	if err := json.Unmarshal([]byte(deployment.Spec.Template.Metadata.Annotations["ad.datadoghq.com/pssst.checks"]), &check); err != nil {
		t.Fatal(err)
	}
	if len(check.OpenMetrics.Instances) != 1 || len(check.OpenMetrics.Instances[0].Metrics) == 0 {
		t.Fatal("expected one instance with an explicit metric list")
	}
	listed := check.OpenMetrics.Instances[0].Metrics

	// The agent configuration for hosts outside Kubernetes must ship the same
	// list, or the two deployments bill and alert differently.
	raw, err = os.ReadFile("../../deploy/datadog/openmetrics.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var agent struct {
		Instances []struct {
			Metrics []string `yaml:"metrics"`
		} `yaml:"instances"`
	}
	if err := yaml.Unmarshal(raw, &agent); err != nil {
		t.Fatal(err)
	}
	if len(agent.Instances) != 1 || !slices.Equal(agent.Instances[0].Metrics, listed) {
		t.Errorf("deploy/datadog/openmetrics.yaml lists %v, the Kubernetes annotation %v", agent.Instances, listed)
	}

	defined := map[string]bool{}
	for _, d := range definitions {
		defined["psp_"+strings.TrimSuffix(d.name, "_total")] = true
		if d.kind == counter && !strings.HasSuffix(d.name, "_total") {
			t.Errorf("counter %s must end in _total", d.name)
		}
	}
	for _, name := range listed {
		if strings.HasSuffix(name, "_total") {
			t.Errorf("%s: list counters without _total, the check skips the suffixed name", name)
		}
		if !defined[name] {
			t.Errorf("%s is not a metric this exporter defines", name)
		}
	}
}
