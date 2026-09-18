package adyen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func fakePage(t *testing.T, body string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+activeIncidentsPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

const quiet = `{"incidentMessageCollection":{"items":[]}}`

func TestQuietPageIsOperational(t *testing.T) {
	// The live shape when Adyen reports no active incident.
	snapshot, err := New(fakePage(t, quiet).URL, nil, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !snapshot.Components["overall"] {
		t.Error("no active incident means operational")
	}
	if len(snapshot.Components) != 1 {
		t.Errorf("Adyen publishes no components, got %d", len(snapshot.Components))
	}
	if len(snapshot.Incidents) != 0 || len(snapshot.Details) != 0 {
		t.Error("nothing should be counted")
	}
}

func TestActiveIncidentIsCounted(t *testing.T) {
	body := `{"incidentMessageCollection":{"items":[
		{"sys":{"id":"abc123"},"title":"Card payments failing","severity":"Degraded performance"},
		{"sys":{"id":"def456"},"title":"Another","severity":"Severely degraded performance"}]}}`

	snapshot, err := New(fakePage(t, body).URL, nil, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snapshot.Components["overall"] {
		t.Error("an active incident is not operational")
	}
	if snapshot.Incidents["minor"] != 1 {
		t.Errorf("degraded performance must be minor, got %d", snapshot.Incidents["minor"])
	}
	if snapshot.Incidents["major"] != 1 {
		t.Errorf("severely degraded must be major, got %d", snapshot.Incidents["major"])
	}
	if len(snapshot.Details) != 2 {
		t.Errorf("details = %d, want 2", len(snapshot.Details))
	}
	for _, incident := range snapshot.Details {
		if incident.ID != "abc123" && incident.ID != "def456" {
			t.Errorf("unexpected incident id %q", incident.ID)
		}
	}
}

func TestUnusableIdentifiersNeverReachLogs(t *testing.T) {
	// An incident still counts, but free text must never become a log field.
	body := `{"incidentMessageCollection":{"items":[
		{"sys":{"id":"payments down <script>alert(1)</script>"},"severity":"unknown wording"},
		{"title":"no identifier at all"}]}}`

	snapshot, err := New(fakePage(t, body).URL, nil, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snapshot.Components["overall"] {
		t.Error("active incidents are not operational")
	}
	if snapshot.Incidents["unknown"] != 2 {
		t.Errorf("unknown severity count = %d, want 2", snapshot.Incidents["unknown"])
	}
	if len(snapshot.Details) != 0 {
		t.Errorf("no usable identifier means no logged detail, got %d", len(snapshot.Details))
	}
}

func TestFetchRejectsUnusableResponses(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":              `<html>`,
		"missing collection":    `{}`,
		"missing items":         `{"incidentMessageCollection":{}}`,
		"items is not a list":   `{"incidentMessageCollection":{"items":{}}}`,
		"collection null":       `{"incidentMessageCollection":null}`,
		"item is not an object": `{"incidentMessageCollection":{"items":["x"]}}`,
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
		http.Error(w, "private detail", http.StatusBadGateway)
	}))
	defer failing.Close()

	if _, err := New(failing.URL, nil, time.Second).Fetch(context.Background()); err == nil {
		t.Error("a failing upstream must fail the snapshot")
	}
	if _, err := New("ftp://example.test", nil, time.Second).Fetch(context.Background()); err == nil {
		t.Error("a non-HTTP base URL must be rejected")
	}
}
