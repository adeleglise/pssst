package paypal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func fakePage(t *testing.T, body string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+eventsPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestClosedEventsAreNotActive(t *testing.T) {
	body := `{"result":[
		{"id":1,"referenceId":"PP-LIVE-1","state":"closed","type":"Incident","environment":"production","severity":"Degraded Performance"},
		{"id":2,"referenceId":"PP-LIVE-2","state":"closed","type":"Maintenance","environment":"production"}]}`

	snap, err := New(fakePage(t, body).URL, nil, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !snap.Components["overall"] {
		t.Error("only closed events means operational")
	}
	if len(snap.Incidents) != 0 || snap.MaintenanceActive != 0 || snap.MaintenanceScheduled != 0 {
		t.Error("a closed event must not be counted")
	}
}

func TestOpenIncidentMakesItNonOperational(t *testing.T) {
	body := `{"result":[
		{"id":1,"referenceId":"PP-LIVE-1","state":"open","type":"Incident","environment":"production","severity":"Degraded Performance"},
		{"id":2,"referenceId":"PP-LIVE-2","state":"open","type":"Incident","environment":"production","severity":null}]}`

	snap, err := New(fakePage(t, body).URL, nil, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Components["overall"] {
		t.Error("an open incident is not operational")
	}
	if snap.Incidents["minor"] != 1 {
		t.Errorf("degraded performance must be minor, got %d", snap.Incidents["minor"])
	}
	if snap.Incidents["unknown"] != 1 {
		t.Errorf("a null severity must be unknown, got %d", snap.Incidents["unknown"])
	}
	if len(snap.Details) != 2 {
		t.Errorf("details = %d, want 2", len(snap.Details))
	}
}

func TestMaintenanceSplitsByStartDate(t *testing.T) {
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	soon := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	later := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"result":[
		{"id":1,"referenceId":"PP-LIVE-1","state":"open","type":"Maintenance","environment":"production","startDate":%q},
		{"id":2,"referenceId":"PP-LIVE-2","state":"open","type":"Maintenance","environment":"production","startDate":%q},
		{"id":3,"referenceId":"PP-LIVE-3","state":"open","type":"Maintenance","environment":"production","startDate":%q}]}`, past, later, soon)

	snap, err := New(fakePage(t, body).URL, nil, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.MaintenanceActive != 1 {
		t.Errorf("a started window is active, got %d", snap.MaintenanceActive)
	}
	if snap.MaintenanceScheduled != 2 {
		t.Errorf("future windows are scheduled, got %d", snap.MaintenanceScheduled)
	}
	if !snap.Components["overall"] {
		t.Error("maintenance alone is not an incident")
	}
	if snap.NextMaintenance.IsZero() || time.Until(snap.NextMaintenance) > 3*time.Hour {
		t.Errorf("next maintenance must be the soonest future window, got %v", snap.NextMaintenance)
	}
}

func TestNonProductionEventsAreIgnored(t *testing.T) {
	// A sandbox incident must never change the production signal.
	body := `{"result":[
		{"id":1,"referenceId":"PP-SANDBOX-1","state":"open","type":"Incident","environment":"sandbox","severity":"Degraded Performance"}]}`

	snap, err := New(fakePage(t, body).URL, nil, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !snap.Components["overall"] {
		t.Error("a sandbox incident must not affect production")
	}
	if len(snap.Incidents) != 0 {
		t.Error("a sandbox incident must not be counted")
	}
}

func TestUnrecognizedStateCountsAsIncident(t *testing.T) {
	body := `{"result":[
		{"id":1,"referenceId":"PP-LIVE-1","state":"investigating","type":"Incident","environment":"production","severity":"Partial Outage"}]}`

	snap, err := New(fakePage(t, body).URL, nil, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Components["overall"] {
		t.Error("an unrecognized state must not be reported operational")
	}
	if len(snap.Incidents) == 0 {
		t.Error("an unrecognized state must still be counted")
	}
}

func TestEmptyStateCountsAsIncident(t *testing.T) {
	body := `{"result":[
		{"id":1,"referenceId":"PP-LIVE-1","state":"","type":"Incident","environment":"production"}]}`

	snap, err := New(fakePage(t, body).URL, nil, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Components["overall"] {
		t.Error("an empty state must not be reported operational")
	}
}

func TestUnrecognizedEnvironmentCountsAsIncident(t *testing.T) {
	body := `{"result":[
		{"id":1,"referenceId":"PP-LIVE-1","state":"open","type":"Incident","environment":"staging","severity":"Partial Outage"}]}`

	snap, err := New(fakePage(t, body).URL, nil, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Components["overall"] {
		t.Error("an unrecognized environment must not be reported operational")
	}
}

func TestEmptyEnvironmentCountsAsIncident(t *testing.T) {
	body := `{"result":[
		{"id":1,"referenceId":"PP-LIVE-1","state":"open","type":"Incident","environment":""}]}`

	snap, err := New(fakePage(t, body).URL, nil, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Components["overall"] {
		t.Error("an empty environment must not be reported operational")
	}
}

func TestFreeTextNeverReachesLogs(t *testing.T) {
	body := `{"result":[
		{"id":1,"referenceId":"card payments <script>","state":"open","type":"Incident","environment":"production"}]}`

	snap, err := New(fakePage(t, body).URL, nil, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Incidents["unknown"] != 1 {
		t.Error("the incident must still be counted")
	}
	if len(snap.Details) != 0 {
		t.Error("an unusable reference must not be logged")
	}
}

func TestFetchRejectsUnusableResponses(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":        `<html>`,
		"missing result":  `{}`,
		"result is null":  `{"result":null}`,
		"result not list": `{"result":{}}`,
		"bad start date":  `{"result":[{"id":1,"referenceId":"PP-LIVE-1","state":"open","type":"Maintenance","environment":"production","startDate":"yesterday"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(fakePage(t, body).URL, nil, time.Second).Fetch(context.Background()); err == nil {
				t.Error("expected an error, got a snapshot")
			}
		})
	}
}

func TestFetchRejectsUpstreamFailure(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private detail", http.StatusServiceUnavailable)
	}))
	defer failing.Close()

	if _, err := New(failing.URL, nil, time.Second).Fetch(context.Background()); err == nil {
		t.Error("a failing upstream must fail the snapshot")
	}
}
