package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetEnforcesItsBounds(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(r.Header.Get("X-Token"))) })
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ok", http.StatusFound) })
	mux.HandleFunc("/error", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	mux.HandleFunc("/large", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", int(MaxBodyBytes)+1)))
	})
	mux.HandleFunc("/slow", func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client := New(200*time.Millisecond, map[string]string{"X-Token": "secret"})
	body, err := client.Get(context.Background(), server.URL+"/ok")
	if err != nil || string(body) != "secret" {
		t.Fatalf("Get(/ok) = %q, %v; want the configured header echoed", body, err)
	}
	for path, want := range map[string]string{
		"/redirect": "HTTP redirect rejected",
		"/error":    "HTTP response status 503",
		"/large":    "HTTP response body too large",
		"/slow":     "HTTP request timed out",
	} {
		_, err := client.Get(context.Background(), server.URL+path)
		if err == nil || err.Error() != want {
			t.Errorf("Get(%s) error = %v, want %q", path, err, want)
		}
		// Errors are local classes only: never the target URL.
		if err != nil && strings.Contains(err.Error(), server.URL) {
			t.Errorf("Get(%s) error leaks the URL: %v", path, err)
		}
	}
}
