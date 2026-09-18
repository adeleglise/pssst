package scheduler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gitea.doudnas.home.al1.io/alain/pssst/internal/cache"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/config"
)

func TestSlowStatusDoesNotBlockProbesOrShutdown(t *testing.T) {
	entered := make(chan struct{}, 1)
	statusServer := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer statusServer.Close()
	var requests atomic.Int64
	probeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, "probe_success 1\nprobe_duration_seconds 0.01\n")
	}))
	defer probeServer.Close()
	cfg := config.Config{Polling: config.Polling{StatusInterval: time.Minute, ProbeInterval: time.Millisecond, Timeout: time.Minute}, Blackbox: config.Blackbox{BaseURL: probeServer.URL}, PSPs: []config.PSP{{ID: "demo", Status: config.Status{Type: "statuspage_v2", BaseURL: statusServer.URL}, Probes: []config.Probe{{ID: "api", Module: "http_2xx", Target: "http://example.invalid"}}}}}
	c := cache.New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { New(cfg, c, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(ctx); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("status poll not started")
	}
	timeout := time.After(time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for requests.Load() < 2 {
		select {
		case <-timeout:
			t.Fatal("slow status blocked probe worker")
		case <-tick.C:
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("inflight requests did not cancel")
	}
}
func TestWorkerNeverOverlapsAndJitterIsBounded(t *testing.T) {
	s := &Scheduler{polling: config.Polling{Jitter: time.Second}}
	for i := 0; i < 100; i++ {
		if d := s.jitter(); d < 0 || d > time.Second {
			t.Fatal("jitter outside configured range")
		}
	}
	var active, maxActive, calls atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := worker{interval: time.Millisecond, poll: func(ctx context.Context) {
		n := active.Add(1)
		if n > maxActive.Load() {
			maxActive.Store(n)
		}
		defer active.Add(-1)
		if calls.Add(1) == 3 {
			cancel()
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Millisecond):
		}
	}}
	s.polling.Jitter = 0
	s.runWorker(ctx, w)
	if maxActive.Load() != 1 || calls.Load() != 3 {
		t.Fatal("workers overlapped or failed to cancel")
	}
}
