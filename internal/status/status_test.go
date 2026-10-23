package status

import (
	"testing"
	"time"
)

func TestEndpoint(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, base, path, want string }{
		{"bare host", "https://status.example.test", "/api/v2/summary.json", "https://status.example.test/api/v2/summary.json"},
		{"trailing slash", "https://status.example.test/", "/api", "https://status.example.test/api"},
		{"base path", "https://status.example.test/sub/", "/api", "https://status.example.test/sub/api"},
		{"query dropped", "https://status.example.test?token=secret", "/api", "https://status.example.test/api"},
		{"fragment dropped", "https://status.example.test/#frag", "/api", "https://status.example.test/api"},
		{"credentials kept for the client to refuse", "http://user:pw@status.example.test", "/api", "http://user:pw@status.example.test/api"},
		{"port", "http://status.example.test:8080", "/api", "http://status.example.test:8080/api"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Endpoint(tc.base, tc.path)
			if err != nil || got != tc.want {
				t.Fatalf("Endpoint(%q, %q) = %q, %v; want %q", tc.base, tc.path, got, err, tc.want)
			}
		})
	}
	for _, base := range []string{"", "status.example.test", "ftp://status.example.test", "file:///etc/passwd", "https://", "http://%zz", "//status.example.test"} {
		if got, err := Endpoint(base, "/api"); err == nil {
			t.Errorf("Endpoint(%q) = %q, want an error", base, got)
		}
	}
}

func TestAddIncidentKeepsOnlySafeIdentifiers(t *testing.T) {
	t.Parallel()
	s := NewSnapshot(true)
	s.AddIncident("abc_123-X", "investigating", "major")
	s.AddIncident("has space", "identified", "major")
	s.AddIncident("Payments are down: see https://x.test", "monitoring", "minor")
	s.AddIncident("", "unknown", "unknown")
	s.AddIncident("a\nb", "unknown", "critical")
	long := make([]byte, 129)
	for i := range long {
		long[i] = 'a'
	}
	s.AddIncident(string(long), "unknown", "unknown")

	want := map[string]int{"major": 2, "minor": 1, "unknown": 2, "critical": 1}
	for severity, n := range want {
		if s.Incidents[severity] != n {
			t.Errorf("Incidents[%q] = %d, want %d", severity, s.Incidents[severity], n)
		}
	}
	if len(s.Details) != 1 || s.Details[0] != (Incident{ID: "abc_123-X", State: "investigating", Severity: "major"}) {
		t.Fatalf("Details = %+v, want only the safe identifier", s.Details)
	}
}

func TestAddScheduledMaintenance(t *testing.T) {
	t.Parallel()
	s := NewSnapshot(true)
	s.AddScheduledMaintenance(time.Time{})
	if s.MaintenanceScheduled != 1 || !s.NextMaintenance.IsZero() {
		t.Fatalf("undated window: scheduled=%d next=%v", s.MaintenanceScheduled, s.NextMaintenance)
	}
	late := time.Date(2030, 1, 2, 3, 0, 0, 0, time.UTC)
	early := late.Add(-48 * time.Hour)
	s.AddScheduledMaintenance(late)
	if !s.NextMaintenance.Equal(late) {
		t.Fatalf("next = %v, want %v", s.NextMaintenance, late)
	}
	s.AddScheduledMaintenance(early)
	s.AddScheduledMaintenance(late.Add(time.Hour))
	s.AddScheduledMaintenance(time.Time{})
	if s.MaintenanceScheduled != 5 || !s.NextMaintenance.Equal(early) {
		t.Fatalf("scheduled=%d next=%v, want 5 and %v", s.MaintenanceScheduled, s.NextMaintenance, early)
	}
	if s.MaintenanceActive != 0 {
		t.Fatalf("a scheduled window must not count as active, got %d", s.MaintenanceActive)
	}
}
