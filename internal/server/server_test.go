package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adeleglise/pssst/internal/cache"
	"github.com/adeleglise/pssst/internal/collector"
	"github.com/adeleglise/pssst/internal/config"
	"github.com/adeleglise/pssst/internal/scheduler"
	"github.com/prometheus/client_golang/prometheus"
)

// A real status HTTP client, Blackbox HTTP client, scheduler, cache, collector
// and HTTP server run together; neither fixture contacts a live PSP.
func TestIndependentSignalsIntegration(t *testing.T) {
	var incident, probeFailed, sourceDown, collectionDown atomic.Bool
	var statusRequests, probeRequests atomic.Int64
	statusServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		statusRequests.Add(1)
		if sourceDown.Load() {
			http.Error(w, "private response", 503)
			return
		}
		indicator, state, incidents := "none", "operational", "[]"
		if incident.Load() {
			indicator, state, incidents = "major", "major_outage", `[{"id":"i1","status":"investigating","impact":"major"}]`
		}
		fmt.Fprintf(w, `{"status":{"indicator":%q},"components":[{"id":"component1","status":%q}],"incidents":%s,"scheduled_maintenances":[]}`, indicator, state, incidents)
	}))
	defer statusServer.Close()
	bbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeRequests.Add(1)
		if collectionDown.Load() {
			http.Error(w, "secret", 503)
			return
		}
		success := 1
		if probeFailed.Load() {
			success = 0
		}
		fmt.Fprintf(w, "# TYPE probe_success gauge\nprobe_success %d\n# TYPE probe_duration_seconds gauge\nprobe_duration_seconds 0.01\n", success)
	}))
	defer bbServer.Close()
	cfg := config.Config{Polling: config.Polling{StatusInterval: 10 * time.Millisecond, ProbeInterval: 10 * time.Millisecond, Timeout: time.Second}, Blackbox: config.Blackbox{BaseURL: bbServer.URL}, PSPs: []config.PSP{{ID: "demo", Status: config.Status{Type: "statuspage_v2", BaseURL: statusServer.URL}, Probes: []config.Probe{{ID: "api", Module: "http_2xx", Target: "http://example.invalid"}}}, {ID: "none", Status: config.Status{Type: "none"}}}}
	c := cache.New(cfg)
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(collector.New(c, "test"))
	srv := httptest.NewServer(Handler(reg, c))
	defer srv.Close()
	fetch := func(path string) (int, string) {
		t.Helper()
		r, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		return r.StatusCode, string(b)
	}
	if code, _ := fetch("/healthz"); code != 200 {
		t.Fatal("healthz")
	}
	if code, _ := fetch("/readyz"); code != 503 {
		t.Fatal("premature readiness")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { scheduler.New(cfg, c, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(ctx); close(done) }()
	eventually := func(f func() bool) {
		t.Helper()
		deadline := time.After(3 * time.Second)
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			if f() {
				return
			}
			select {
			case <-deadline:
				t.Fatal("state did not converge")
			case <-tick.C:
			}
		}
	}
	eventually(func() bool { s := c.Snapshot()[0]; return c.Ready() && s.Status.Up && s.Probes["api"].Up })
	incident.Store(true)
	eventually(func() bool {
		s := c.Snapshot()[0]
		return !s.Status.Data.Components["overall"] && s.Probes["api"].Data.Success
	})
	incident.Store(false)
	probeFailed.Store(true)
	eventually(func() bool {
		s := c.Snapshot()[0]
		return s.Status.Data.Components["overall"] && !s.Probes["api"].Data.Success && s.Probes["api"].Up
	})
	sourceDown.Store(true)
	collectionDown.Store(true)
	eventually(func() bool { s := c.Snapshot()[0]; return !s.Status.Up && !s.Probes["api"].Up })
	code, body := fetch("/metrics")
	if code != 200 || !strings.Contains(body, `psp_declared_operational{component="overall",psp="demo"} 1`) || !strings.Contains(body, `psp_probe_success{endpoint="api",psp="demo"} 0`) {
		t.Fatalf("metrics unavailable or lost cached data: %s", body)
	}
	if code, _ := fetch("/readyz"); code != 200 {
		t.Fatal("upstream outage removed readiness")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler shutdown timed out")
	}
	if code, _ := fetch("/readyz"); code != 503 {
		t.Fatal("ready during shutdown")
	}
	beforeS, beforeP := statusRequests.Load(), probeRequests.Load()
	for i := 0; i < 10; i++ {
		if code, _ := fetch("/metrics"); code != 200 {
			t.Fatal("metrics failed after scheduler stopped")
		}
	}
	if statusRequests.Load() != beforeS || probeRequests.Load() != beforeP {
		t.Fatal("scrapes performed external I/O")
	}
}
