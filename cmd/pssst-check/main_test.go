package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInventoryAuditReportsEveryProvider(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"components":[{"id":"cmp1","status":"operational"},{"id":"cmp2","status":"major_outage"}],
			"incidents":[{"id":"inc1","status":"investigating","impact":"major"}],
			"status":{"indicator":"major"}
		}`))
	}))
	defer page.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer broken.Close()

	path := filepath.Join(t.TempDir(), "inventory.yml")
	inventory := fmt.Sprintf(`psps:
  - id: healthy_page
    status: {type: statuspage_v2, base_url: %q, components: {api: cmp1, payouts: cmp2}}
  - id: broken_page
    status: {type: statuspage_v2, base_url: %q}
  - id: silent
    status: {type: none}
`, page.URL, broken.URL)
	if err := os.WriteFile(path, []byte(inventory), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-config", path}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit = %d, want 1 when a source fails; stderr: %s", code, stderr.String())
	}
	want := []string{
		"healthy_page\tstatuspage_v2\tok\tNOT operational\tpayouts\tmajor:1\t0 active, 0 scheduled",
		"broken_page\tstatuspage_v2\terror: HTTP response status 502",
		"silent\tnone\tabsent",
	}
	for _, line := range want {
		if !strings.Contains(stdout.String(), line) {
			t.Errorf("output lacks %q:\n%s", line, stdout.String())
		}
	}
}

func TestSingleSourceNeedsAURL(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != exitUsage {
		t.Fatalf("exit = %d, want usage error", code)
	}
}
