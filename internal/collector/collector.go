// Package collector exports cache snapshots only. Collect never performs I/O.
package collector

import (
	"time"

	"gitea.doudnas.home.al1.io/alain/pssst/internal/cache"
	"gitea.doudnas.home.al1.io/alain/pssst/internal/status"
	"github.com/prometheus/client_golang/prometheus"
)

type Collector struct {
	cache   *cache.Cache
	version string
	desc    map[string]*prometheus.Desc
}

type definition struct {
	name, help string
	labels     []string
}

var definitions = []definition{
	{"exporter_build_info", "Build information.", []string{"version"}},
	{"info", "Entity class of a monitored provider, always 1.", []string{"psp", "kind"}},
	{"status_source_configured", "Whether an official status source is configured.", []string{"psp"}},
	{"status_source_up", "Whether the last status poll returned a complete valid snapshot.", []string{"psp"}},
	{"status_source_last_success_timestamp_seconds", "Unix timestamp of the last valid status snapshot, or zero.", []string{"psp"}},
	{"status_source_last_poll_timestamp_seconds", "Unix timestamp of the last completed status poll, or zero.", []string{"psp"}},
	{"status_source_stale_after_seconds", "Maximum status snapshot age used by example rules.", []string{"psp"}},
	{"declared_operational", "Whether a declared component is operational (last valid snapshot).", []string{"psp", "component"}},
	{"active_incidents", "Unresolved incidents by normalized severity (last valid snapshot).", []string{"psp", "severity"}},
	{"maintenance_active", "Active maintenance events (last valid snapshot).", []string{"psp"}},
	{"maintenance_scheduled", "Scheduled maintenance events (last valid snapshot).", []string{"psp"}},
	{"maintenance_next_start_timestamp_seconds", "Unix timestamp of the earliest scheduled maintenance, or zero.", []string{"psp"}},
	{"probe_collection_up", "Whether the last attempt collected a valid Blackbox response.", []string{"psp", "endpoint"}},
	{"probe_last_poll_timestamp_seconds", "Unix timestamp of the last completed probe collection attempt, or zero.", []string{"psp", "endpoint"}},
	{"probe_last_success_timestamp_seconds", "Unix timestamp of the last successful observed probe, or zero.", []string{"psp", "endpoint"}},
	{"probe_last_collection_success_timestamp_seconds", "Unix timestamp of the last valid Blackbox response, or zero.", []string{"psp", "endpoint"}},
	{"probe_stale_after_seconds", "Maximum probe observation age used by example rules.", []string{"psp", "endpoint"}},
	{"probe_success", "Whether the last collected probe succeeded.", []string{"psp", "endpoint"}},
	{"probe_duration_seconds", "Duration of the last collected probe in seconds.", []string{"psp", "endpoint"}},
	{"probe_http_status_code", "HTTP status code from the last collected HTTP probe (zero if no response).", []string{"psp", "endpoint"}},
	{"probe_ssl_earliest_cert_expiry_timestamp_seconds", "Unix timestamp of the earliest certificate expiry from the last collected TLS probe.", []string{"psp", "endpoint"}},
}

func New(c *cache.Cache, version string) *Collector {
	result := &Collector{cache: c, version: version, desc: make(map[string]*prometheus.Desc)}
	for _, d := range definitions {
		prefix := "psp_"
		result.desc[d.name] = prometheus.NewDesc(prefix+d.name, d.help, d.labels, nil)
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
		ch <- prometheus.MustNewConstMetric(c.desc[name], prometheus.GaugeValue, value, labels...)
	}
	emit("exporter_build_info", 1, c.version)
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
