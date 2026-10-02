// Package collector exports cache snapshots only. Collect never performs I/O.
package collector

import (
	"time"

	"github.com/adeleglise/pssst/internal/cache"
	"github.com/adeleglise/pssst/internal/status"
	"github.com/prometheus/client_golang/prometheus"
)

type Collector struct {
	cache   *cache.Cache
	version string
	now     func() time.Time
	desc    map[string]*prometheus.Desc
	kind    map[string]prometheus.ValueType
}

type definition struct {
	name, help string
	labels     []string
	// kind is always explicit: the zero ValueType is invalid, and a scraper
	// that reads a counter as a gauge silently loses every rate it could
	// compute.
	kind prometheus.ValueType
}

const (
	gauge   = prometheus.GaugeValue
	counter = prometheus.CounterValue
)

var definitions = []definition{
	{"exporter_build_info", "Build information.", []string{"version"}, gauge},
	{"info", "Entity class of a monitored provider, always 1.", []string{"psp", "kind"}, gauge},
	{"status_source_configured", "Whether an official status source is configured.", []string{"psp"}, gauge},
	{"status_source_up", "Whether the last status poll returned a complete valid snapshot.", []string{"psp"}, gauge},
	{"status_source_last_success_timestamp_seconds", "Unix timestamp of the last valid status snapshot, or zero.", []string{"psp"}, gauge},
	{"status_source_last_poll_timestamp_seconds", "Unix timestamp of the last completed status poll, or zero.", []string{"psp"}, gauge},
	{"status_source_stale_after_seconds", "Maximum status snapshot age used by example rules.", []string{"psp"}, gauge},
	{"status_source_fresh", "Whether the last valid status snapshot is younger than stale_after.", []string{"psp"}, gauge},
	{"declared_operational", "Whether a declared component is operational (last valid snapshot).", []string{"psp", "component"}, gauge},
	{"active_incidents", "Unresolved incidents by normalized severity (last valid snapshot).", []string{"psp", "severity"}, gauge},
	{"maintenance_active", "Active maintenance events (last valid snapshot).", []string{"psp"}, gauge},
	{"maintenance_scheduled", "Scheduled maintenance events (last valid snapshot).", []string{"psp"}, gauge},
	{"maintenance_next_start_timestamp_seconds", "Unix timestamp of the earliest scheduled maintenance, or zero.", []string{"psp"}, gauge},
	{"probe_collection_up", "Whether the last attempt collected a valid Blackbox response.", []string{"psp", "endpoint"}, gauge},
	{"probe_last_poll_timestamp_seconds", "Unix timestamp of the last completed probe collection attempt, or zero.", []string{"psp", "endpoint"}, gauge},
	{"probe_last_success_timestamp_seconds", "Unix timestamp of the last successful observed probe, or zero.", []string{"psp", "endpoint"}, gauge},
	{"probe_last_collection_success_timestamp_seconds", "Unix timestamp of the last valid Blackbox response, or zero.", []string{"psp", "endpoint"}, gauge},
	{"probe_stale_after_seconds", "Maximum probe observation age used by example rules.", []string{"psp", "endpoint"}, gauge},
	{"probe_fresh", "Whether the last valid Blackbox response is younger than stale_after.", []string{"psp", "endpoint"}, gauge},
	{"probe_success", "Whether the last collected probe succeeded.", []string{"psp", "endpoint"}, gauge},
	{"probe_duration_seconds", "Duration of the last collected probe in seconds.", []string{"psp", "endpoint"}, gauge},
	{"probe_http_status_code", "HTTP status code from the last collected HTTP probe (zero if no response).", []string{"psp", "endpoint"}, gauge},
	{"probe_ssl_earliest_cert_expiry_timestamp_seconds", "Unix timestamp of the earliest certificate expiry from the last collected TLS probe.", []string{"psp", "endpoint"}, gauge},
	{"status_poll_total", "Total declared-status polls by outcome.", []string{"psp", "outcome"}, counter},
	{"probe_collection_total", "Total Blackbox collection attempts by outcome.", []string{"psp", "endpoint", "outcome"}, counter},
	{"probe_result_total", "Total observed probe results by outcome, counted only when collection succeeded.", []string{"psp", "endpoint", "outcome"}, counter},
}

const (
	outcomeSuccess = "success"
	outcomeFailure = "failure"
)

func New(c *cache.Cache, version string) *Collector {
	result := &Collector{
		cache:   c,
		version: version,
		now:     time.Now,
		desc:    make(map[string]*prometheus.Desc),
		kind:    make(map[string]prometheus.ValueType),
	}
	for _, d := range definitions {
		prefix := "psp_"
		result.desc[d.name] = prometheus.NewDesc(prefix+d.name, d.help, d.labels, nil)
		result.kind[d.name] = d.kind
	}
	return result
}
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range definitions {
		ch <- c.desc[d.name]
	}
}
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	emit := func(name string, value float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(c.desc[name], c.kind[name], value, labels...)
	}
	emit("exporter_build_info", 1, c.version)
	now := c.now()
	for _, s := range c.cache.Snapshot() {
		id := s.ID
		source := s.Status
		emit("info", 1, id, s.Kind)
		emit("status_source_configured", boolValue(source.Configured), id)
		if source.Configured {
			emit("status_source_up", boolValue(source.Up), id)
			emit("status_source_last_success_timestamp_seconds", timestamp(source.LastSuccess), id)
			emit("status_source_last_poll_timestamp_seconds", timestamp(source.LastPoll), id)
			emit("status_source_stale_after_seconds", source.StaleAfter.Seconds(), id)
			emit("status_source_fresh", boolValue(fresh(source.LastSuccess, source.StaleAfter, now)), id)
			emit("status_poll_total", float64(source.PollSuccess), id, outcomeSuccess)
			emit("status_poll_total", float64(source.PollFailure), id, outcomeFailure)
			if source.HasData {
				for component, operational := range source.Data.Components {
					emit("declared_operational", boolValue(operational), id, component)
				}
				for _, severity := range status.Severities {
					emit("active_incidents", float64(source.Data.Incidents[severity]), id, severity)
				}
				emit("maintenance_active", float64(source.Data.MaintenanceActive), id)
				emit("maintenance_scheduled", float64(source.Data.MaintenanceScheduled), id)
				emit("maintenance_next_start_timestamp_seconds", timestamp(source.Data.NextMaintenance), id)
			}
		}
		for endpoint, p := range s.Probes {
			emit("probe_collection_up", boolValue(p.Up), id, endpoint)
			emit("probe_last_poll_timestamp_seconds", timestamp(p.LastPoll), id, endpoint)
			emit("probe_last_success_timestamp_seconds", timestamp(p.LastSuccess), id, endpoint)
			emit("probe_last_collection_success_timestamp_seconds", timestamp(p.LastCollectionSuccess), id, endpoint)
			emit("probe_stale_after_seconds", p.StaleAfter.Seconds(), id, endpoint)
			emit("probe_fresh", boolValue(fresh(p.LastCollectionSuccess, p.StaleAfter, now)), id, endpoint)
			emit("probe_collection_total", float64(p.CollectionSuccess), id, endpoint, outcomeSuccess)
			emit("probe_collection_total", float64(p.CollectionFailure), id, endpoint, outcomeFailure)
			emit("probe_result_total", float64(p.ResultSuccess), id, endpoint, outcomeSuccess)
			emit("probe_result_total", float64(p.ResultFailure), id, endpoint, outcomeFailure)
			if !p.HasData {
				continue
			}
			emit("probe_success", boolValue(p.Data.Success), id, endpoint)
			emit("probe_duration_seconds", p.Data.DurationSeconds, id, endpoint)
			if p.Data.HTTPStatusCode != nil {
				emit("probe_http_status_code", *p.Data.HTTPStatusCode, id, endpoint)
			}
			if p.Data.EarliestCertExpiry != nil {
				emit("probe_ssl_earliest_cert_expiry_timestamp_seconds", *p.Data.EarliestCertExpiry, id, endpoint)
			}
		}
	}
}

// fresh applies the rules' freshness test inside the exporter, for consumers
// that cannot compare a timestamp with the current time. Datadog is one: its
// metric queries have no time(), so without this gauge a last known good value
// would read as current forever. It judges one signal, never both together.
func fresh(lastSuccess time.Time, staleAfter time.Duration, now time.Time) bool {
	return !lastSuccess.IsZero() && now.Sub(lastSuccess) <= staleAfter
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
func timestamp(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.UnixNano()) / 1e9
}
