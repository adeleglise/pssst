package kener

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// monitorBlock reproduces the markup Kener emits for one component, including
// the ninety daily history bars that reuse the same colour classes as the
// current state. A parser that matches on proximity would read those instead.
func monitorBlock(name, state string) string {
	var history strings.Builder
	for day := 0; day < 90; day++ {
		history.WriteString(fmt.Sprintf(`<div data-ts="%d" class="bg-api-down h-[30px] w-[8px]"></div>`, 1782000000+day*86400))
	}
	return fmt.Sprintf(`<div class="monitor relative grid w-full grid-cols-12 gap-2">
		<div class="col-span-12"><div class="pt-0"><div class="scroll-m-20 pr-5 text-xl font-medium tracking-tight">
		<p class="overflow-hidden text-ellipsis whitespace-nowrap">%s</p>
		<p class="mt-1 text-xs font-medium text-muted-foreground"></p></div></div></div>
		<div class="col-span-12 flex">%s</div>
		<div class="col-span-12"><span class="text-api-%s text-sm">Status OK</span></div>
	</div>`, name, history.String(), state)
}

func pageWith(blocks ...string) string {
	legend := `<section class="section-legend"><span class="bg-api-up"></span><span>UP</span>
		<span class="bg-api-degraded"></span><span>DEGRADED</span>
		<span class="bg-api-down"></span><span>DOWN</span>
		<span class="bg-api-maintenance"></span><span>MAINTENANCE</span></section>`
	return `<!DOCTYPE html><html><body><main class="kener-theme-mono">` + legend + strings.Join(blocks, "") + `</main></body></html>`
}

func serve(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestHealthyPageIsOperational(t *testing.T) {
	body := pageWith(monitorBlock("Authentication", "up"), monitorBlock("Card payments", "up"))
	server := serve(t, body)

	snapshot, err := New(server.URL, nil, map[string]string{"auth": "Authentication"}, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !snapshot.Components["overall"] {
		t.Error("all monitors up means operational; history bars must not be read as state")
	}
	if !snapshot.Components["auth"] {
		t.Error("mapped monitor must be operational")
	}
	if len(snapshot.Components) != 2 {
		t.Errorf("only overall and mapped monitors belong here, got %d", len(snapshot.Components))
	}
}

func TestDegradedMonitorIsNotOperational(t *testing.T) {
	body := pageWith(monitorBlock("Authentication", "up"), monitorBlock("Card payments", "degraded"))
	server := serve(t, body)

	snapshot, err := New(server.URL, nil, map[string]string{"cards": "Card payments", "auth": "Authentication"}, time.Second).Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snapshot.Components["overall"] {
		t.Error("a degraded monitor makes the page non-operational")
	}
	if snapshot.Components["cards"] {
		t.Error("the degraded monitor must report its own state")
	}
	if !snapshot.Components["auth"] {
		t.Error("a healthy monitor stays operational")
	}
}

func TestOnlyUpIsOperational(t *testing.T) {
	for _, state := range []string{"down", "degraded", "maintenance", "something-new"} {
		t.Run(state, func(t *testing.T) {
			server := serve(t, pageWith(monitorBlock("API", state)))
			snapshot, err := New(server.URL, nil, nil, time.Second).Fetch(context.Background())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if snapshot.Components["overall"] {
				t.Errorf("state %q must not be operational", state)
			}
		})
	}
}

func TestMarkupChangeFailsLoudly(t *testing.T) {
	// A redesign must take the source down, never report a healthy page.
	for name, body := range map[string]string{
		"no monitor block":    `<html><body><main class="kener-theme-mono"><p>Everything is fine</p></main></body></html>`,
		"block without name":  `<html><body><div class="monitor relative"><span class="text-api-up">Status OK</span></div></body></html>`,
		"block without state": `<html><body><div class="monitor relative"><p class="overflow-hidden text-ellipsis whitespace-nowrap">API</p></div></body></html>`,
		"empty document":      ``,
	} {
		t.Run(name, func(t *testing.T) {
			server := serve(t, body)
			if _, err := New(server.URL, nil, nil, time.Second).Fetch(context.Background()); err == nil {
				t.Error("expected an error, got a snapshot")
			}
		})
	}
}

func TestDuplicateAndMissingMonitors(t *testing.T) {
	duplicate := pageWith(monitorBlock("API", "up"), monitorBlock("API", "down"))
	if _, err := New(serve(t, duplicate).URL, nil, nil, time.Second).Fetch(context.Background()); err == nil {
		t.Error("two monitors with the same name must fail rather than pick one")
	}

	page := pageWith(monitorBlock("API", "up"))
	if _, err := New(serve(t, page).URL, nil, map[string]string{"x": "Absent"}, time.Second).Fetch(context.Background()); err == nil {
		t.Error("a configured monitor missing upstream must fail the snapshot")
	}
}

func TestFetchRejectsUpstreamFailure(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private detail", http.StatusBadGateway)
	}))
	defer failing.Close()

	if _, err := New(failing.URL, nil, nil, time.Second).Fetch(context.Background()); err == nil {
		t.Error("a failing upstream must fail the snapshot")
	}
	if _, err := New("ftp://example.test", nil, nil, time.Second).Fetch(context.Background()); err == nil {
		t.Error("a non-HTTP base URL must be rejected")
	}
}
