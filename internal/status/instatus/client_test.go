package instatus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakePage serves both Instatus documents; either can be replaced per test.
func fakePage(t *testing.T, summary, components string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+summaryPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(summary))
	})
	mux.HandleFunc("GET "+componentsPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(components))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

const healthySummary = `{"page":{"name":"Demo","url":"https://example.test","status":"UP"},"activeIncidents":[],"activeMaintenances":[]}`
const healthyComponents = `{"components":[{"id":"cmp1","name":"API","status":"OPERATIONAL"},{"id":"cmp2","name":"Checkout","status":"OPERATIONAL"}]}`

func TestFetchNormalizesHealthyPage(t *testing.T) {
	server := fakePage(t, healthySummary, healthyComponents)
	client := New(server.URL, nil, map[string]string{"payment_api": "cmp1"}, time.Second)

	snapshot, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !snapshot.Components["overall"] {
		t.Error("overall must be operational for an UP page with healthy components")
	}
	if !snapshot.Components["payment_api"] {
		t.Error("mapped component must be operational")
	}
	if len(snapshot.Components) != 2 {
		t.Errorf("only overall and mapped components belong in the snapshot, got %d", len(snapshot.Components))
	}
	for severity, count := range snapshot.Incidents {
		t.Errorf("unexpected incident %s=%d", severity, count)
	}
}

func TestOverallFollowsDegradedComponent(t *testing.T) {
	// A contradictory page rollup must not hide an explicitly failed component.
	components := `{"components":[{"id":"cmp1","name":"API","status":"MAJOROUTAGE"}]}`
	server := fakePage(t, healthySummary, components)
	client := New(server.URL, nil, map[string]string{"payment_api": "cmp1"}, time.Second)

	snapshot, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snapshot.Components["overall"] {
		t.Error("overall must be non-operational when a component fails")
	}
	if snapshot.Components["payment_api"] {
		t.Error("mapped component must report its own failure")
	}
}

func TestIncidentsAndMaintenanceAreNormalized(t *testing.T) {
	summary := `{"page":{"name":"Demo","url":"https://example.test","status":"HASISSUES"},
	"activeIncidents":[
	  {"id":"inc1","name":"payment failures","status":"INVESTIGATING","impact":"MAJOROUTAGE"},
	  {"id":"inc2","name":"latency","status":"MONITORING"},
	  {"id":"inc3","name":"old","status":"RESOLVED","impact":"MINOR"}],
	"activeMaintenances":[
	  {"id":"mnt1","name":"later","status":"NOTSTARTEDYET","start":"2030-01-02T03:04:05.000Z"},
	  {"id":"mnt2","name":"sooner","status":"NOTSTARTEDYET","start":"2029-01-02T03:04:05.000Z"},
	  {"id":"mnt3","name":"running","status":"INPROGRESS","start":"2020-01-02T03:04:05.000Z"},
	  {"id":"mnt4","name":"done","status":"COMPLETED","start":"2019-01-02T03:04:05.000Z"}]}`
	server := fakePage(t, summary, healthyComponents)
	client := New(server.URL, nil, nil, time.Second)

	snapshot, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snapshot.Components["overall"] {
		t.Error("HASISSUES must not be operational")
	}
	if snapshot.Incidents["major"] != 1 {
		t.Errorf("major incidents = %d, want 1", snapshot.Incidents["major"])
	}
	if snapshot.Incidents["unknown"] != 1 {
		t.Errorf("an incident without impact must be unknown, got %d", snapshot.Incidents["unknown"])
	}
	if snapshot.Incidents["minor"] != 0 {
		t.Error("a resolved incident must not be counted")
	}
	if len(snapshot.Details) != 2 {
		t.Errorf("details = %d, want 2 unresolved incidents", len(snapshot.Details))
	}
	if snapshot.MaintenanceScheduled != 2 {
		t.Errorf("scheduled maintenance = %d, want 2", snapshot.MaintenanceScheduled)
	}
	if snapshot.MaintenanceActive != 1 {
		t.Errorf("active maintenance = %d, want 1", snapshot.MaintenanceActive)
	}
	wantNext := time.Date(2029, 1, 2, 3, 4, 5, 0, time.UTC)
	if !snapshot.NextMaintenance.Equal(wantNext) {
		t.Errorf("next maintenance = %v, want the earliest scheduled %v", snapshot.NextMaintenance, wantNext)
	}
}

func TestAbsentIncidentListsAreEmpty(t *testing.T) {
	// A real quiet Instatus page omits both lists entirely; that is not an error.
	summary := `{"page":{"name":"Demo","url":"https://example.test","status":"UP"}}`
	server := fakePage(t, summary, healthyComponents)
	client := New(server.URL, nil, nil, time.Second)

	snapshot, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !snapshot.Components["overall"] {
		t.Error("a quiet page is operational")
	}
	if len(snapshot.Details) != 0 || snapshot.MaintenanceActive != 0 || snapshot.MaintenanceScheduled != 0 {
		t.Error("absent lists must normalize to nothing active")
	}
}

func TestUnknownStatesAreNonOperational(t *testing.T) {
	summary := `{"page":{"name":"Demo","url":"https://example.test","status":"SOMETHINGNEW"},"activeIncidents":[],"activeMaintenances":[]}`
	components := `{"components":[{"id":"cmp1","name":"API","status":"SOMETHINGNEW"}]}`
	server := fakePage(t, summary, components)
	client := New(server.URL, nil, map[string]string{"payment_api": "cmp1"}, time.Second)

	snapshot, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snapshot.Components["overall"] || snapshot.Components["payment_api"] {
		t.Error("an unknown state must never be reported operational")
	}
}

func TestFetchRejectsIncompleteDocuments(t *testing.T) {
	cases := map[string]struct{ summary, components string }{
		"summary not JSON":      {`not json`, healthyComponents},
		"components not JSON":   {healthySummary, `not json`},
		"missing page status":   {`{"page":{"name":"Demo"},"activeIncidents":[],"activeMaintenances":[]}`, healthyComponents},
		"missing components":    {healthySummary, `{}`},
		"component without id":  {healthySummary, `{"components":[{"name":"API","status":"OPERATIONAL"}]}`},
		"duplicate component":   {healthySummary, `{"components":[{"id":"cmp1","status":"OPERATIONAL"},{"id":"cmp1","status":"OPERATIONAL"}]}`},
		"incident without id":   {`{"page":{"status":"UP"},"activeIncidents":[{"status":"INVESTIGATING"}],"activeMaintenances":[]}`, healthyComponents},
		"unparsable start date": {`{"page":{"status":"UP"},"activeIncidents":[],"activeMaintenances":[{"id":"m1","status":"NOTSTARTEDYET","start":"yesterday"}]}`, healthyComponents},
		"unsafe entity id":      {healthySummary, `{"components":[{"id":"cmp 1 <script>","status":"OPERATIONAL"}]}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server := fakePage(t, tc.summary, tc.components)
			client := New(server.URL, nil, nil, time.Second)
			if _, err := client.Fetch(context.Background()); err == nil {
				t.Error("expected an error, got a snapshot")
			}
		})
	}
}

func TestFetchRejectsMissingMappedComponent(t *testing.T) {
	server := fakePage(t, healthySummary, healthyComponents)
	client := New(server.URL, nil, map[string]string{"payment_api": "absent"}, time.Second)

	if _, err := client.Fetch(context.Background()); err == nil {
		t.Error("a configured component missing upstream must fail the whole snapshot")
	}
}

func TestFetchRejectsUpstreamFailures(t *testing.T) {
	// Either document failing must fail the snapshot atomically.
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == summaryPath {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(healthySummary))
			return
		}
		http.Error(w, "private detail", http.StatusServiceUnavailable)
	}))
	defer failing.Close()

	client := New(failing.URL, nil, nil, time.Second)
	if _, err := client.Fetch(context.Background()); err == nil {
		t.Error("a failing components document must fail the snapshot")
	}

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(healthySummary))
	}))
	defer slow.Close()

	timedOut := New(slow.URL, nil, nil, 20*time.Millisecond)
	if _, err := timedOut.Fetch(context.Background()); err == nil {
		t.Error("a timeout must fail the snapshot")
	}
}

func TestFetchRejectsInvalidBaseURL(t *testing.T) {
	client := New("ftp://example.test", nil, nil, time.Second)
	if _, err := client.Fetch(context.Background()); err == nil {
		t.Error("a non-HTTP base URL must be rejected")
	}
}

func TestCancelledContextIsNotASnapshot(t *testing.T) {
	server := fakePage(t, healthySummary, healthyComponents)
	client := New(server.URL, nil, nil, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := client.Fetch(ctx); err == nil {
		t.Error("a cancelled context must not produce a snapshot")
	}
}

// Instatus can likewise publish a maintenance without a start date.
func TestMaintenanceWithoutStartDate(t *testing.T) {
	summary := `{"page":{"status":"UP"},"activeMaintenances":[
		{"id":"undated","status":"NOTSTARTEDYET"},
		{"id":"dated","status":"NOTSTARTEDYET","start":"2030-04-05T06:07:08.000Z"}]}`
	server := fakePage(t, summary, healthyComponents)

	snapshot, err := New(server.URL, nil, nil, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("a missing start must not fail the snapshot: %v", err)
	}
	if snapshot.MaintenanceScheduled != 2 {
		t.Errorf("scheduled maintenance = %d, want 2", snapshot.MaintenanceScheduled)
	}
	want := time.Date(2030, 4, 5, 6, 7, 8, 0, time.UTC)
	if !snapshot.NextMaintenance.Equal(want) {
		t.Errorf("next maintenance = %v, want %v", snapshot.NextMaintenance, want)
	}
}
