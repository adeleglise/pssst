package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFakePSPControlsSignalsIndependently(t *testing.T) {
	s := newServer()
	h := httptest.NewServer(s)
	defer h.Close()

	get := func(path string) *http.Response {
		t.Helper()
		response, err := h.Client().Get(h.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	post := func(path string) *http.Response {
		t.Helper()
		response, err := h.Client().Post(h.URL+path, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	response := get("/api/v2/summary.json")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("initial status = %d", response.StatusCode)
	}
	response.Body.Close()
	response = get("/payment-api/health")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("initial API status = %d", response.StatusCode)
	}
	response.Body.Close()

	response = post("/control?incident=true")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("incident control = %d", response.StatusCode)
	}
	response.Body.Close()
	response = get("/api/v2/summary.json")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status endpoint = %d", response.StatusCode)
	}
	body := readBody(t, response)
	if !strings.Contains(body, `"impact":"major"`) || !strings.Contains(body, `"status":"degraded_performance"`) {
		t.Fatalf("incident status not represented: %s", body)
	}
	response = get("/payment-api/health")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("incident unexpectedly changed API status = %d", response.StatusCode)
	}
	response.Body.Close()

	response = post("/control?api_failure=true")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("API control = %d", response.StatusCode)
	}
	response.Body.Close()
	response = get("/payment-api/health")
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("failed API status = %d", response.StatusCode)
	}
	response.Body.Close()

	response = get("/control?api_failure=false")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET control status = %d", response.StatusCode)
	}
	response.Body.Close()
	response = get("/payment-api/health")
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET control mutated API state = %d", response.StatusCode)
	}
	response.Body.Close()
}

func TestFakePSPRejectsInvalidControls(t *testing.T) {
	s := newServer()
	h := httptest.NewServer(s)
	defer h.Close()
	response, err := h.Client().Post(h.URL+"/control?incident=maybe", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid control = %d", response.StatusCode)
	}
}
