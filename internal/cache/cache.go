// Package cache stores only the configured inventory. Poll failures update
// collection health, never erase the previous valid observation.
package cache

import (
	"errors"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/adeleglise/pssst/internal/blackbox"
	"github.com/adeleglise/pssst/internal/config"
	"github.com/adeleglise/pssst/internal/status"
)

type StatusState struct {
	Configured  bool
	Up          bool
	HasData     bool
	LastPoll    time.Time
	LastSuccess time.Time
	StaleAfter  time.Duration
	Data        status.Snapshot
	// Cumulative poll outcomes. They only ever grow, so a scraper can derive
	// an error rate that a gauge of the last outcome cannot express.
	PollSuccess uint64
	PollFailure uint64
}
type ProbeState struct {
	Up                    bool
	HasData               bool
	LastPoll              time.Time
	LastSuccess           time.Time
	LastCollectionSuccess time.Time
	StaleAfter            time.Duration
	Data                  blackbox.Result
	// Collection counts every attempt to reach Blackbox; result counts only
	// the probes Blackbox actually reported on. Keeping them apart is what
	// separates "we could not measure" from "the provider failed".
	CollectionSuccess uint64
	CollectionFailure uint64
	ResultSuccess     uint64
	ResultFailure     uint64
}
type PSPState struct {
	ID     string
	Kind   string
	Status StatusState
	Probes map[string]ProbeState
}
type Cache struct {
	mu      sync.RWMutex
	states  map[string]PSPState
	order   []string
	stopped bool
}

func New(cfg config.Config) *Cache {
	c := &Cache{states: make(map[string]PSPState, len(cfg.PSPs))}
	for _, p := range cfg.PSPs {
		s := PSPState{ID: p.ID, Kind: p.Kind, Status: StatusState{Configured: p.Status.Type != config.StatusTypeNone, StaleAfter: 3 * (cfg.Polling.StatusInterval + cfg.Polling.Jitter + cfg.Polling.Timeout)}, Probes: make(map[string]ProbeState, len(p.Probes))}
		for _, probe := range p.Probes {
			s.Probes[probe.ID] = ProbeState{StaleAfter: 3 * (cfg.Polling.ProbeInterval + cfg.Polling.Jitter + cfg.Polling.Timeout)}
		}
		c.states[p.ID] = s
		c.order = append(c.order, p.ID)
	}
	return c
}
func (c *Cache) UpdateStatus(id string, data status.Snapshot, up bool, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.states[id]
	if !ok || !s.Status.Configured {
		return errors.New("status source not in configured inventory")
	}
	s.Status.LastPoll = now
	s.Status.Up = up
	if up {
		s.Status.Data = cloneStatus(data)
		s.Status.HasData = true
		s.Status.LastSuccess = now
		s.Status.PollSuccess++
	} else {
		s.Status.PollFailure++
	}
	c.states[id] = s
	return nil
}
func (c *Cache) UpdateProbe(id, endpoint string, data blackbox.Result, up bool, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.states[id]
	if !ok {
		return errors.New("PSP not in configured inventory")
	}
	p, ok := s.Probes[endpoint]
	if !ok {
		return errors.New("probe not in configured inventory")
	}
	p.LastPoll = now
	p.Up = up
	if !up {
		p.CollectionFailure++
	} else {
		p.Data = cloneProbe(data)
		p.HasData = true
		p.LastCollectionSuccess = now
		p.CollectionSuccess++
		if data.Success {
			p.LastSuccess = now
			p.ResultSuccess++
		} else {
			p.ResultFailure++
		}
	}
	s.Probes[endpoint] = p
	return nil
}
func (c *Cache) Snapshot() []PSPState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]PSPState, 0, len(c.states))
	for _, id := range c.order {
		s := c.states[id]
		s.Status.Data = cloneStatus(s.Status.Data)
		s.Probes = maps.Clone(s.Probes)
		for id, p := range s.Probes {
			p.Data = cloneProbe(p.Data)
			s.Probes[id] = p
		}
		result = append(result, s)
	}
	return result
}
func (c *Cache) Ready() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.stopped {
		return false
	}
	for _, s := range c.states {
		if s.Status.Configured && s.Status.LastPoll.IsZero() {
			return false
		}
		for _, p := range s.Probes {
			if p.LastPoll.IsZero() {
				return false
			}
		}
	}
	return true
}
func (c *Cache) Stop() { c.mu.Lock(); defer c.mu.Unlock(); c.stopped = true }
func cloneStatus(s status.Snapshot) status.Snapshot {
	s.Components = maps.Clone(s.Components)
	s.Incidents = maps.Clone(s.Incidents)
	s.Details = slices.Clone(s.Details)
	return s
}
func cloneProbe(p blackbox.Result) blackbox.Result {
	if p.HTTPStatusCode != nil {
		v := *p.HTTPStatusCode
		p.HTTPStatusCode = &v
	}
	if p.EarliestCertExpiry != nil {
		v := *p.EarliestCertExpiry
		p.EarliestCertExpiry = &v
	}
	return p
}
