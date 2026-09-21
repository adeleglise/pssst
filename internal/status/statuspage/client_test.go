package statuspage_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adeleglise/pssst/internal/status"
	"github.com/adeleglise/pssst/internal/status/statuspage"
)

func TestFetchNormalizesCompleteSummary(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/summary.json" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("authorization header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"components":[
				{"id":"payments","status":"operational"},
				{"id":"refunds","status":"degraded_performance"}
			],
			"incidents":[
				{"id":"incident-active","status":"identified","impact":"major"},
				{"id":"incident-resolved","status":"resolved","impact":"critical"}
			],
			"scheduled_maintenances":[
				{"id":"maintenance-active","status":"in_progress","scheduled_for":"2026-09-18T10:00:00Z"},
				{"id":"maintenance-later","status":"scheduled","scheduled_for":"2026-09-19T10:00:00Z"}
			],
			"status":{"indicator":"minor"}
		}`))
	}))
	defer server.Close()

	client := statuspage.New(server.URL, map[string]string{"Authorization": "Bearer secret"}, map[string]string{
		"payments_api": "payments",
		"refunds_api":  "refunds",
	}, time.Second)

	got, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	wantComponents := map[string]bool{"overall": false, "payments_api": true, "refunds_api": false}
	if !equalComponents(got.Components, wantComponents) {
		t.Errorf("components = %#v, want %#v", got.Components, wantComponents)
	}
	if got.Incidents["major"] != 1 || len(got.Incidents) != 1 {
		t.Errorf("incidents = %#v, want one major incident", got.Incidents)
	}
	if got.MaintenanceActive != 1 || got.MaintenanceScheduled != 1 {
		t.Errorf("maintenance = active %d scheduled %d, want 1 and 1", got.MaintenanceActive, got.MaintenanceScheduled)
	}
	wantNext := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	if !got.NextMaintenance.Equal(wantNext) {
		t.Errorf("next maintenance = %s, want %s", got.NextMaintenance, wantNext)
	}
	wantDetails := []status.Incident{{ID: "incident-active", State: "identified", Severity: "major"}}
	if !equalDetails(got.Details, wantDetails) {
		t.Errorf("details = %#v, want %#v", got.Details, wantDetails)
	}
}

func TestFetchRejectsIncompleteOrUnsafeSummary(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"missing components":       `{"incidents":[],"scheduled_maintenances":[],"status":{"indicator":"none"}}`,
		"missing page indicator":   `{"components":[],"incidents":[],"scheduled_maintenances":[]}`,
		"duplicate component":      `{"components":[{"id":"payments","status":"operational"},{"id":"payments","status":"operational"}],"incidents":[],"scheduled_maintenances":[],"status":{"indicator":"none"}}`,
		"duplicate incident":       `{"components":[],"incidents":[{"id":"one","status":"identified","impact":"major"},{"id":"one","status":"monitoring","impact":"major"}],"scheduled_maintenances":[],"status":{"indicator":"none"}}`,
		"invalid maintenance time": `{"components":[],"incidents":[],"scheduled_maintenances":[{"id":"one","status":"scheduled","scheduled_for":"not-a-time"}],"status":{"indicator":"none"}}`,
	}
	for name, payload := range tests {
		name, payload := name, payload
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(payload))
			}))
			defer server.Close()
			_, err := statuspage.New(server.URL, nil, nil, time.Second).Fetch(context.Background())
			if err == nil {
				t.Fatal("Fetch() error = nil, want rejected summary")
			}
			if len(err.Error()) > 128 || strings.Contains(err.Error(), server.URL) {
				t.Errorf("error is not bounded and sanitized: %q", err)
			}
		})
	}
}

func TestFetchRejectsMissingMappedComponent(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"components":[],"incidents":[],"scheduled_maintenances":[],"status":{"indicator":"none"}}`))
	}))
	defer server.Close()

	_, err := statuspage.New(server.URL, nil, map[string]string{"payment_api": "missing"}, time.Second).Fetch(context.Background())
	if err == nil {
		t.Fatal("Fetch() error = nil, want missing component error")
	}
}

func TestFetchKeepsUnknownStatesNonOperational(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"components":[{"id":"payments","status":"new-state"}],"incidents":[],"scheduled_maintenances":[],"status":{"indicator":"new-indicator"}}`))
	}))
	defer server.Close()

	snapshot, err := statuspage.New(server.URL, nil, map[string]string{"payments_api": "payments"}, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if snapshot.Components["overall"] || snapshot.Components["payments_api"] {
		t.Errorf("unknown states must not be operational: %#v", snapshot.Components)
	}
}

func TestFetchRejectsHTTPFailuresTimeoutsAndOversizeResponses(t *testing.T) {
	t.Parallel()
	tests := map[string]http.HandlerFunc{
		"http failure": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) },
		"redirect": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "/next")
			w.WriteHeader(http.StatusFound)
		},
		"oversize": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(fmt.Sprintf(`{"components":[],"incidents":[],"scheduled_maintenances":[],"status":{"indicator":"none"}}%s`, strings.Repeat(" ", 2<<20))))
		},
		"timeout": func(_ http.ResponseWriter, _ *http.Request) { time.Sleep(100 * time.Millisecond) },
	}
	for name, handler := range tests {
		name, handler := name, handler
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(handler)
			defer server.Close()
			timeout := time.Second
			if name == "timeout" {
				timeout = 10 * time.Millisecond
			}
			_, err := statuspage.New(server.URL, nil, nil, timeout).Fetch(context.Background())
			if err == nil {
				t.Fatal("Fetch() error = nil")
			}
		})
	}
}

func equalComponents(got, want map[string]bool) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}

func equalDetails(got, want []status.Incident) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestOverallRejectsContradictoryComponentState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":{"indicator":"none"},"components":[{"id":"api","status":"major_outage"}],"incidents":[],"scheduled_maintenances":[]}`))
	}))
	defer server.Close()
	snapshot, err := statuspage.New(server.URL, nil, nil, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Components["overall"] {
		t.Fatal("overall marked healthy despite an explicit failed component")
	}
}
func TestIncidentIDCannotCarryUntrustedTextIntoLogs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":{"indicator":"none"},"components":[],"incidents":[{"id":"https://user:secret@example.test","status":"investigating","impact":"major"}],"scheduled_maintenances":[]}`))
	}))
	defer server.Close()
	if _, err := statuspage.New(server.URL, nil, nil, time.Second).Fetch(context.Background()); err == nil {
		t.Fatal("unsafe incident ID accepted for structured logs")
	}
}

// A real quiet Statuspage omits the empty incidents key entirely. Observed on
// status.sumup.com: keys are components, page, scheduled_maintenances, status.
func TestAbsentEmptyListsAreNotAFailure(t *testing.T) {
	t.Parallel()

	fetch := func(t *testing.T, body string) (status.Snapshot, error) {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(server.Close)
		return statuspage.New(server.URL, nil, nil, time.Second).Fetch(context.Background())
	}

	snapshot, err := fetch(t, `{"page":{"id":"p1"},"status":{"indicator":"none","description":"All Systems Operational"},
		"components":[{"id":"cmp1","status":"operational"}],"scheduled_maintenances":[]}`)
	if err != nil {
		t.Fatalf("an omitted empty incidents list must not fail the snapshot: %v", err)
	}
	if !snapshot.Components["overall"] {
		t.Error("overall must be operational")
	}
	if len(snapshot.Details) != 0 {
		t.Error("no incident expected")
	}

	// The same holds for an omitted scheduled_maintenances key.
	if _, err := fetch(t, `{"page":{"id":"p1"},"status":{"indicator":"none"},"components":[{"id":"cmp1","status":"operational"}]}`); err != nil {
		t.Fatalf("an omitted maintenance list must not fail the snapshot: %v", err)
	}

	// Components and the page indicator stay mandatory: their absence means a
	// truncated or foreign document, never a healthy provider.
	if _, err := fetch(t, `{"page":{"id":"p1"},"status":{"indicator":"none"},"incidents":[]}`); err == nil {
		t.Error("a summary without components must fail")
	}
	if _, err := fetch(t, `{"page":{"id":"p1"},"components":[],"incidents":[]}`); err == nil {
		t.Error("a summary without a page indicator must fail")
	}
}

// Observed on status.sumup.com and www.gocardless-status.com: a scheduled
// maintenance can carry a null date. It still counts, but it cannot be the
// next start, and it must never invalidate the whole snapshot.
func TestScheduledMaintenanceWithoutDate(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"page":{"id":"p1"},"status":{"indicator":"none"},"components":[],
		"scheduled_maintenances":[
			{"id":"undated","status":"scheduled","scheduled_for":null},
			{"id":"dated","status":"scheduled","scheduled_for":"2030-04-05T06:07:08Z"}]}`))
	}))
	defer server.Close()

	snapshot, err := statuspage.New(server.URL, nil, nil, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("a null scheduled_for must not fail the snapshot: %v", err)
	}
	if snapshot.MaintenanceScheduled != 2 {
		t.Errorf("scheduled maintenance = %d, want 2", snapshot.MaintenanceScheduled)
	}
	want := time.Date(2030, 4, 5, 6, 7, 8, 0, time.UTC)
	if !snapshot.NextMaintenance.Equal(want) {
		t.Errorf("next maintenance = %v, want the only dated window %v", snapshot.NextMaintenance, want)
	}
}
