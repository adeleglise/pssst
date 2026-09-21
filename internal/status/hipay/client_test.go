package hipay

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// page builds one monitor-list response for the given page number.
func page(monitors string, total, perPage int) string {
	return fmt.Sprintf(`{"status":"ok","data":[%s],"psp":{"totalMonitors":%d,"perPage":%d}}`, monitors, total, perPage)
}

const healthy = `{"monitorId":1001,"statusClass":"success","name":"PROD - API"},{"monitorId":1002,"statusClass":"success","name":"PROD - Console"}`

func serve(t *testing.T, bodies map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested := r.URL.Query().Get("page")
		body, found := bodies[requested]
		if !found {
			http.Error(w, "no such page", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestHealthyMonitorsAreOperational(t *testing.T) {
	server := serve(t, map[string]string{"1": page(healthy, 2, 50)})

	snapshot, err := New(server.URL, nil, map[string]string{"api": "1001"}, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !snapshot.Components["overall"] || !snapshot.Components["api"] {
		t.Error("every monitor in success means operational")
	}
	if len(snapshot.Components) != 2 {
		t.Errorf("only overall and mapped components belong here, got %d", len(snapshot.Components))
	}
}

func TestAnyFailingMonitorIsNotOperational(t *testing.T) {
	failing := `{"monitorId":1001,"statusClass":"danger","name":"PROD - API"},{"monitorId":1002,"statusClass":"success","name":"PROD - Console"}`
	server := serve(t, map[string]string{"1": page(failing, 2, 50)})

	snapshot, err := New(server.URL, nil, map[string]string{"api": "1001", "console": "1002"}, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snapshot.Components["overall"] {
		t.Error("a failing monitor makes the page non-operational")
	}
	if snapshot.Components["api"] {
		t.Error("the failing monitor must report its own state")
	}
	if !snapshot.Components["console"] {
		t.Error("a healthy monitor stays operational")
	}
}

func TestUnknownStatusClassIsNotOperational(t *testing.T) {
	server := serve(t, map[string]string{"1": page(`{"monitorId":1001,"statusClass":"brand-new","name":"X"}`, 1, 50)})

	snapshot, err := New(server.URL, nil, nil, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snapshot.Components["overall"] {
		t.Error("only success is operational; anything else is not")
	}
}

func TestPaginationIsFollowed(t *testing.T) {
	first := page(`{"monitorId":1001,"statusClass":"success","name":"A"}`, 2, 1)
	second := page(`{"monitorId":1002,"statusClass":"danger","name":"B"}`, 2, 1)
	server := serve(t, map[string]string{"1": first, "2": second})

	snapshot, err := New(server.URL, nil, map[string]string{"b": "1002"}, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snapshot.Components["overall"] {
		t.Error("a failure on the second page must still be seen")
	}
	if snapshot.Components["b"] {
		t.Error("the second page monitor must be mapped")
	}
}

func TestPaginationIsBounded(t *testing.T) {
	// A page that always claims more monitors must not loop forever.
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(page(`{"monitorId":1001,"statusClass":"success","name":"A"}`, 100000, 1)))
	}))
	defer server.Close()

	if _, err := New(server.URL, nil, nil, time.Second).Fetch(context.Background()); err == nil {
		t.Error("an endless monitor list must be rejected")
	}
	if got := requests.Load(); got > maxPages {
		t.Errorf("requests = %d, must stay within %d pages", got, maxPages)
	}
}

func TestMissingTotalFails(t *testing.T) {
	server := serve(t, map[string]string{"1": `{"status":"ok","data":[{"monitorId":1001,"statusClass":"success"}]}`})

	if _, err := New(server.URL, nil, nil, time.Second).Fetch(context.Background()); err == nil {
		t.Error("a missing monitor total must fail the snapshot")
	}
}

func TestZeroTotalFails(t *testing.T) {
	server := serve(t, map[string]string{"1": page(`{"monitorId":1001,"statusClass":"success"}`, 0, 50)})

	if _, err := New(server.URL, nil, nil, time.Second).Fetch(context.Background()); err == nil {
		t.Error("a zero monitor total must fail the snapshot")
	}
}

func TestEmptyDataWithoutTotalFails(t *testing.T) {
	server := serve(t, map[string]string{"1": `{"status":"ok","data":[]}`})

	if _, err := New(server.URL, nil, nil, time.Second).Fetch(context.Background()); err == nil {
		t.Error("an empty monitor list without a total must fail the snapshot")
	}
}

func TestPageAddingNothingFails(t *testing.T) {
	server := serve(t, map[string]string{
		"1": page(`{"monitorId":1001,"statusClass":"success"}`, 2, 1),
		"2": page(``, 2, 1),
	})

	if _, err := New(server.URL, nil, nil, time.Second).Fetch(context.Background()); err == nil {
		t.Error("a page adding no monitor before the total must fail the snapshot")
	}
}

func TestCountExceedingTotalFails(t *testing.T) {
	monitors := `{"monitorId":1001,"statusClass":"success"},{"monitorId":1002,"statusClass":"success"}`
	server := serve(t, map[string]string{"1": page(monitors, 1, 50)})

	if _, err := New(server.URL, nil, nil, time.Second).Fetch(context.Background()); err == nil {
		t.Error("a monitor count exceeding the declared total must fail the snapshot")
	}
}

func TestChangingTotalFails(t *testing.T) {
	// The total drops to match what is already collected: a naive reader
	// would treat this as complete after one real monitor out of five.
	server := serve(t, map[string]string{
		"1": page(`{"monitorId":1001,"statusClass":"success"}`, 5, 1),
		"2": page(``, 1, 1),
	})

	if _, err := New(server.URL, nil, nil, time.Second).Fetch(context.Background()); err == nil {
		t.Error("a total that changes between pages must fail the snapshot")
	}
}

func TestMissingMappedMonitorFails(t *testing.T) {
	server := serve(t, map[string]string{"1": page(healthy, 2, 50)})

	if _, err := New(server.URL, nil, map[string]string{"api": "9999"}, time.Second).Fetch(context.Background()); err == nil {
		t.Error("a configured monitor missing upstream must fail the snapshot")
	}
}

func TestFetchRejectsUnusableResponses(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":           `<html>`,
		"missing data":       `{"status":"ok"}`,
		"data is null":       `{"status":"ok","data":null}`,
		"status not ok":      `{"status":"error","data":[]}`,
		"monitor without id": `{"status":"ok","data":[{"statusClass":"success","name":"A"}],"psp":{"totalMonitors":1,"perPage":50}}`,
		"duplicate monitor":  `{"status":"ok","data":[{"monitorId":1,"statusClass":"success"},{"monitorId":1,"statusClass":"success"}],"psp":{"totalMonitors":2,"perPage":50}}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := serve(t, map[string]string{"1": body})
			if _, err := New(server.URL, nil, nil, time.Second).Fetch(context.Background()); err == nil {
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

	if _, err := New(failing.URL, nil, nil, time.Second).Fetch(context.Background()); err == nil {
		t.Error("a failing upstream must fail the snapshot")
	}
	if _, err := New("ftp://example.test", nil, nil, time.Second).Fetch(context.Background()); err == nil {
		t.Error("a non-HTTP base URL must be rejected")
	}
}
