// Package scheduler runs one non-overlapping loop per configured signal. The
// fixed inventory bounds goroutines; a slow provider cannot block another PSP.
package scheduler

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"gitea.doudnas.home.al1.io/alain/pssst/internal/blackbox"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/cache"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/config"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/status"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/status/adyen"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/status/hipay"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/status/instatus"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/status/kener"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/status/paypal"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/status/statuspage"
)

type worker struct {
	interval time.Duration
	poll     func(context.Context)
}
type Scheduler struct {
	cache   *cache.Cache
	polling config.Polling
	workers []worker
}

func New(cfg config.Config, c *cache.Cache, logger *slog.Logger) *Scheduler {
	s := &Scheduler{cache: c, polling: cfg.Polling}
	// Share one transport for all probes; each Statuspage has its own credentials.
	var probes blackbox.ProbeProvider = blackbox.New(cfg.Blackbox.BaseURL, cfg.Blackbox.Headers, cfg.Polling.Timeout)
	for _, psp := range cfg.PSPs {
		if provider, ok := statusProvider(psp, cfg.Polling.Timeout); ok {
			previous := map[string]status.Incident{}
			s.workers = append(s.workers, worker{interval: cfg.Polling.StatusInterval, poll: func(ctx context.Context) {
				data, err := provider.Fetch(ctx)
				if ctx.Err() != nil {
					return
				}
				if updateErr := c.UpdateStatus(psp.ID, data, err == nil, time.Now()); updateErr != nil {
					logger.Error("cache update rejected", "psp", psp.ID, "signal", "status")
					return
				}
				if err != nil {
					logger.Warn("status collection failed", "psp", psp.ID, "error_class", "upstream_or_payload")
					return
				}
				// Only bounded adapter-normalized details enter logs, never incident prose.
				current := make(map[string]status.Incident, len(data.Details))
				for _, incident := range data.Details {
					current[incident.ID] = incident
					if old, ok := previous[incident.ID]; !ok || old != incident {
						logger.Info("declared incident updated", "psp", psp.ID, "incident_id", incident.ID, "state", incident.State, "severity", incident.Severity)
					}
				}
				for id := range previous {
					if _, ok := current[id]; !ok {
						logger.Info("declared incident no longer active", "psp", psp.ID, "incident_id", id)
					}
				}
				previous = current
			}})
		}
		for _, probe := range psp.Probes {
			s.workers = append(s.workers, worker{interval: cfg.Polling.ProbeInterval, poll: func(ctx context.Context) {
				data, err := probes.Probe(ctx, probe.Module, probe.Target)
				if ctx.Err() != nil {
					return
				}
				if updateErr := c.UpdateProbe(psp.ID, probe.ID, data, err == nil, time.Now()); updateErr != nil {
					logger.Error("cache update rejected", "psp", psp.ID, "endpoint", probe.ID, "signal", "probe")
					return
				}
				if err != nil {
					logger.Warn("probe collection failed", "psp", psp.ID, "endpoint", probe.ID, "error_class", "upstream_or_payload")
				}
			}})
		}
	}
	return s
}

// statusProvider builds the declared-status adapter for one PSP. A PSP without
// an official source has no worker at all, which the cache reports as
// unconfigured rather than healthy.
func statusProvider(psp config.PSP, timeout time.Duration) (status.StatusProvider, bool) {
	switch psp.Status.Type {
	case config.StatusTypeStatuspageV2:
		return statuspage.New(psp.Status.BaseURL, psp.Status.Headers, psp.Status.Components, timeout), true
	case config.StatusTypeInstatusV1:
		return instatus.New(psp.Status.BaseURL, psp.Status.Headers, psp.Status.Components, timeout), true
	case config.StatusTypeHiPayV1:
		return hipay.New(psp.Status.BaseURL, psp.Status.Headers, psp.Status.Components, timeout), true
	case config.StatusTypeKenerV1:
		return kener.New(psp.Status.BaseURL, psp.Status.Headers, psp.Status.Components, timeout), true
	case config.StatusTypeAdyenV1:
		return adyen.New(psp.Status.BaseURL, psp.Status.Headers, timeout), true
	case config.StatusTypePayPalV1:
		return paypal.New(psp.Status.BaseURL, psp.Status.Headers, timeout), true
	default:
		return nil, false
	}
}

// Run blocks until all workers have stopped. A Scheduler is run exactly once.
func (s *Scheduler) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, w := range s.workers {
		wg.Add(1)
		go func() { defer wg.Done(); s.runWorker(ctx, w) }()
	}
	<-ctx.Done()
	s.cache.Stop()
	wg.Wait()
}
func (s *Scheduler) runWorker(ctx context.Context, w worker) {
	delay := s.jitter()
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		// Providers enforce a request deadline. The parent remains the cancellation
		// signal so timed-out attempts still update cache health and readiness.
		w.poll(ctx)
		delay = w.interval + s.jitter()
	}
}
func (s *Scheduler) jitter() time.Duration {
	if s.polling.Jitter <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(s.polling.Jitter) + 1))
}
