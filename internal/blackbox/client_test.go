package blackbox_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitea.doudnas.home.al1.io/alain/pssst/internal/blackbox"
)

func TestProbeMapsAllowedScalarMetrics(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/probe" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("target"); got != "https://api.example.test/a path?x=1&y=2" {
			t.Fatalf("encoded target decoded as %q", got)
		}
		if got := r.URL.Query().Get("module"); got != "http 2xx" {
			t.Fatalf("module = %q", got)
		}
		if got := r.Header.Get("X-Adapter-Token"); got != "secret" {
			t.Fatalf("header = %q", got)
		}
		_, _ = w.Write([]byte(`# TYPE probe_success gauge
probe_success 0
# TYPE probe_duration_seconds gauge
probe_duration_seconds 2.5
# TYPE probe_http_status_code gauge
probe_http_status_code 503
# TYPE probe_ssl_earliest_cert_expiry gauge
probe_ssl_earliest_cert_expiry 1795000000
# TYPE ignored_metric gauge
ignored_metric{unbounded="ignored"} 123
`))
	}))
	defer server.Close()

	client := blackbox.New(server.URL, map[string]string{"X-Adapter-Token": "secret"}, time.Second)
	got, err := client.Probe(context.Background(), "http 2xx", "https://api.example.test/a path?x=1&y=2")
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if got.Success {
		t.Error("Success = true, want false for probe_success 0")
	}
	if got.DurationSeconds != 2.5 {
		t.Errorf("DurationSeconds = %v, want 2.5", got.DurationSeconds)
	}
	if got.HTTPStatusCode == nil || *got.HTTPStatusCode != 503 {
		t.Errorf("HTTPStatusCode = %v, want 503", got.HTTPStatusCode)
	}
	if got.EarliestCertExpiry == nil || *got.EarliestCertExpiry != 1795000000 {
		t.Errorf("EarliestCertExpiry = %v, want 1795000000", got.EarliestCertExpiry)
	}
}

func TestProbeDistinguishesProbeFailureFromCollectionFailure(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("probe_success 0\nprobe_duration_seconds 0.01\n"))
	}))
	defer server.Close()

	result, err := blackbox.New(server.URL, nil, time.Second).Probe(context.Background(), "http_2xx", "https://api.example.test")
	if err != nil {
		t.Fatalf("Probe() error = %v; a reported probe failure is still a collection success", err)
	}
	if result.Success || result.DurationSeconds != 0.01 || result.HTTPStatusCode != nil || result.EarliestCertExpiry != nil {
		t.Errorf("result = %#v, want failed probe with only required metrics", result)
	}
}

func TestProbeRejectsInvalidSelectedMetrics(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"missing success":     "probe_duration_seconds 1\n",
		"missing duration":    "probe_success 1\n",
		"labelled success":    "probe_success{target=\"leak\"} 1\nprobe_duration_seconds 1\n",
		"duplicate duration":  "probe_success 1\nprobe_duration_seconds 1\nprobe_duration_seconds 2\n",
		"nonfinite duration":  "probe_success 1\nprobe_duration_seconds NaN\n",
		"invalid success":     "probe_success 2\nprobe_duration_seconds 1\n",
		"invalid http code":   "probe_success 1\nprobe_duration_seconds 1\nprobe_http_status_code 200.5\n",
		"invalid cert expiry": "probe_success 1\nprobe_duration_seconds 1\nprobe_ssl_earliest_cert_expiry -1\n",
		"labelled optional":   "probe_success 1\nprobe_duration_seconds 1\nprobe_http_status_code{instance=\"leak\"} 200\n",
		"duplicate optional":  "probe_success 1\nprobe_duration_seconds 1\nprobe_http_status_code 200\nprobe_http_status_code 201\n",
	}
	for name, metrics := range tests {
		name, metrics := name, metrics
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(metrics))
			}))
			defer server.Close()

			_, err := blackbox.New(server.URL, nil, time.Second).Probe(context.Background(), "http_2xx", "https://api.example.test")
			if err == nil {
				t.Fatal("Probe() error = nil, want invalid collection")
			}
		})
	}
}

func TestProbeRejectsUnavailableTimeoutAndOversizeCollection(t *testing.T) {
	t.Parallel()
	tests := map[string]http.HandlerFunc{
		"http failure": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) },
		"redirect": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "/probe")
			w.WriteHeader(http.StatusFound)
		},
		"oversize": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(fmt.Sprintf("probe_success 1\\nprobe_duration_seconds 1\\n%s", strings.Repeat("#", 2<<20))))
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
			_, err := blackbox.New(server.URL, nil, timeout).Probe(context.Background(), "http_2xx", "https://api.example.test")
			if err == nil {
				t.Fatal("Probe() error = nil")
			}
		})
	}
}

func TestHTTPStatusMustBeZeroOrValidCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("probe_success 1\nprobe_duration_seconds 0.01\nprobe_http_status_code 9999\n"))
	}))
	defer server.Close()
	if _, err := blackbox.New(server.URL, nil, time.Second).Probe(context.Background(), "http_2xx", "https://example.test"); err == nil {
		t.Fatal("invalid HTTP status accepted")
	}
}
