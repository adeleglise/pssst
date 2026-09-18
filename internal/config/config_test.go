package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const valid = `psps:
  - id: demo
    status:
      type: statuspage_v2
      base_url: https://status.example.test
      components:
        payment_api: abc123
    probes:
      - id: api
        module: http_2xx
        target: https://api.example.test/health
blackbox:
  base_url: http://localhost:9115
`

func TestDefaults(t *testing.T) {
	c, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if c.Polling.StatusInterval != time.Minute || c.Polling.Timeout != 10*time.Second || c.Server.ListenAddress != ":9099" {
		t.Fatalf("wrong defaults: %+v", c.Polling)
	}
	if c.PSPs[0].Status.Components["payment_api"] != "abc123" {
		t.Fatal("lost mapping")
	}
}
func TestRejectInvalidConfig(t *testing.T) {
	cases := map[string]string{
		"unknown":                  valid + "typo: true\n",
		"nested unknown":           strings.Replace(valid, "module: http_2xx", "modul: http_2xx", 1),
		"duplicate PSP":            valid + "psps: []\n",
		"duplicate endpoint":       strings.Replace(valid, "blackbox:", "      - id: api\n        module: http_2xx\n        target: https://example.test\nblackbox:", 1),
		"invalid adapter":          strings.Replace(valid, "statuspage_v2", "html", 1),
		"missing adapter":          strings.Replace(valid, "type: statuspage_v2", "type: ''", 1),
		"bad url":                  strings.Replace(valid, "https://status.example.test", "ftp://example.test", 1),
		"credentials":              strings.Replace(valid, "https://status.example.test", "https://user:SECRET@example.test", 1),
		"no PSPs":                  "psps: []\n",
		"negative":                 "polling:\n  timeout: -1s\n" + valid,
		"zero":                     "polling:\n  status_interval: 0s\n" + valid,
		"timeout exceeds interval": "polling:\n  timeout: 90s\n" + valid,
		"bad duration":             "polling:\n  timeout: banana\n" + valid,
		"document":                 valid + "---\n" + valid,
		"empty document":           valid + "---\n",
		"bad target":               strings.Replace(valid, "https://api.example.test/health", "abc", 1),
		"bad module":               strings.Replace(valid, "http_2xx", "http&module=x", 1),
		"bad ID":                   strings.Replace(valid, "id: demo", "id: '../SECRET'", 1),
		"reserved component":       strings.Replace(valid, "payment_api: abc123", "overall: abc123", 1),
		"none with url":            strings.Replace(valid, "statuspage_v2", "none", 1),
		"missing environment":      strings.Replace(valid, "abc123", "${PSSST_NONEXISTENT_VALUE_729}", 1),
		"unsafe header":            "blackbox:\n  base_url: http://localhost:9115\n  headers:\n    Host: bad\npsps:\n  - id: demo\n    status: {type: none}\n",
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(s))
			if err == nil {
				t.Fatal("accepted invalid configuration")
			}
			if strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("secret in error: %v", err)
			}
		})
	}
}
func TestEnvironmentExpansionIsScalarAndSecretSafe(t *testing.T) {
	t.Setenv("PSSST_TEST_TOKEN", "secret: value\npsps: []")
	s := valid + "server:\n  listen_address: ${PSSST_TEST_TOKEN}\n"
	if _, err := Parse([]byte(s)); err == nil || strings.Contains(err.Error(), "secret:") {
		t.Fatalf("unsafe validation: %v", err)
	}
	t.Setenv("PSSST_TEST_TOKEN", "Bearer opaque:token # value")
	s = strings.Replace(valid, "base_url: http://localhost:9115", "base_url: http://localhost:9115\n  headers:\n    Authorization: ${PSSST_TEST_TOKEN}", 1)
	c, err := Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	if c.Blackbox.Headers["Authorization"] != "Bearer opaque:token # value" {
		t.Fatal("scalar was interpreted as YAML")
	}
}
func TestNoneAndTCPTarget(t *testing.T) {
	s := `blackbox:
  base_url: http://localhost:9115
psps:
  - id: none
    status: {type: none}
    probes:
      - {id: tcp, module: tcp_connect, target: '[::1]:443'}
`
	if _, err := Parse([]byte(s)); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryAndInputBounds(t *testing.T) {
	t.Run("PSP count", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("psps:\n")
		for i := 0; i < 129; i++ {
			fmt.Fprintf(&b, "  - id: p%d\n    status: {type: none}\n", i)
		}
		if _, err := Parse([]byte(b.String())); err == nil {
			t.Fatal("unbounded PSP inventory accepted")
		}
	})
	t.Run("duplicate PSP IDs", func(t *testing.T) {
		if _, err := Parse([]byte("psps:\n  - id: duplicate\n    status: {type: none}\n  - id: duplicate\n    status: {type: none}\n")); err == nil {
			t.Fatal("duplicate ID accepted")
		}
	})
	t.Run("probe count", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("blackbox:\n  base_url: http://localhost:9115\npsps:\n  - id: demo\n    status: {type: none}\n    probes:\n")
		for i := 0; i < 33; i++ {
			fmt.Fprintf(&b, "      - {id: p%d, module: tcp_connect, target: 'localhost:443'}\n", i)
		}
		if _, err := Parse([]byte(b.String())); err == nil {
			t.Fatal("unbounded probe inventory accepted")
		}
	})
	t.Run("file size", func(t *testing.T) {
		if _, err := Parse([]byte(strings.Repeat(" ", (1<<20)+1))); err == nil {
			t.Fatal("oversize config accepted")
		}
	})
	t.Run("alias", func(t *testing.T) {
		if _, err := Parse([]byte("psps:\n  - &a {id: demo, status: {type: none}}\n  - *a\n")); err == nil {
			t.Fatal("alias accepted")
		}
	})
}
