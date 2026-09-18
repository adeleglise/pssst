// Package config reads a bounded, strictly validated configuration. Error messages
// identify fields rather than echoing values, which can contain credentials.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const MaxConfigBytes = 1 << 20
const MaxPSPs = 128
const MaxProbes = 32
const MaxComponents = 128

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,63}$`)

// Kinds is the bounded set of entity classes exported by psp_info.
var Kinds = [...]string{KindPSP, "acquirer", "bank"}

const KindPSP = "psp"

const (
	StatusTypeNone         = "none"
	StatusTypeStatuspageV2 = "statuspage_v2"
	StatusTypeInstatusV1   = "instatus_v1"
	StatusTypeAdyenV1      = "adyen_v1"
	StatusTypePayPalV1     = "paypal_v1"
	StatusTypeHiPayV1      = "hipay_v1"
)

var envReference = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)
var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

type Config struct {
	Server   Server   `yaml:"server"`
	Polling  Polling  `yaml:"polling"`
	Blackbox Blackbox `yaml:"blackbox"`
	PSPs     []PSP    `yaml:"psps"`
}
type Server struct {
	ListenAddress string `yaml:"listen_address"`
}
type Polling struct {
	StatusInterval time.Duration `yaml:"status_interval"`
	ProbeInterval  time.Duration `yaml:"probe_interval"`
	Timeout        time.Duration `yaml:"timeout"`
	Jitter         time.Duration `yaml:"jitter"`
}
type Blackbox struct {
	BaseURL string            `yaml:"base_url"`
	Headers map[string]string `yaml:"headers"`
}
type PSP struct {
	ID          string  `yaml:"id"`
	DisplayName string  `yaml:"display_name"`
	Kind        string  `yaml:"kind"`
	Status      Status  `yaml:"status"`
	Probes      []Probe `yaml:"probes"`
}
type Status struct {
	Type       string            `yaml:"type"`
	BaseURL    string            `yaml:"base_url"`
	Headers    map[string]string `yaml:"headers"`
	Components map[string]string `yaml:"components"`
}
type Probe struct {
	ID     string `yaml:"id"`
	Module string `yaml:"module"`
	Target string `yaml:"target"`
}

func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, errors.New("cannot open configuration file")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if err != nil {
		return Config{}, errors.New("cannot read configuration file")
	}
	return Parse(b)
}

func Parse(b []byte) (Config, error) {
	fail := func(s string) (Config, error) { return Config{}, errors.New(s) }
	if len(b) > MaxConfigBytes {
		return fail("configuration exceeds 1 MiB")
	}
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&doc); err != nil {
		return fail("invalid YAML configuration")
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return fail("configuration must contain exactly one YAML document")
	}
	if err := expand(&doc, false); err != nil {
		return Config{}, err
	}
	expanded, err := yaml.Marshal(&doc)
	if err != nil {
		return fail("invalid YAML configuration")
	}
	c := Config{Server: Server{ListenAddress: ":9099"}, Polling: Polling{StatusInterval: time.Minute, ProbeInterval: 30 * time.Second, Timeout: 10 * time.Second, Jitter: 5 * time.Second}}
	strict := yaml.NewDecoder(bytes.NewReader(expanded))
	strict.KnownFields(true)
	if err := strict.Decode(&c); err != nil {
		return fail("configuration has unknown, duplicate, or invalid fields")
	}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	// Validation accepts an empty kind; the default is applied once, here.
	for i := range c.PSPs {
		if c.PSPs[i].Kind == "" {
			c.PSPs[i].Kind = KindPSP
		}
	}
	return c, nil
}

func expand(n *yaml.Node, key bool) error {
	if n.Kind == yaml.AliasNode {
		return errors.New("YAML aliases are not supported")
	}
	if n.Kind == yaml.ScalarNode && n.Tag == "!!str" && !key {
		missing := false
		n.Value = envReference.ReplaceAllStringFunc(n.Value, func(ref string) string {
			v, ok := os.LookupEnv(ref[2 : len(ref)-1])
			if !ok {
				missing = true
			}
			return v
		})
		if missing {
			return errors.New("configuration references an unset environment variable")
		}
	}
	for i, child := range n.Content {
		if err := expand(child, n.Kind == yaml.MappingNode && i%2 == 0); err != nil {
			return err
		}
	}
	return nil
}

func (c Config) validate() error {
	_, port, err := net.SplitHostPort(c.Server.ListenAddress)
	if err != nil || !validPort(port) {
		return errors.New("server.listen_address must be host:port with a numeric port")
	}
	for _, d := range []time.Duration{c.Polling.StatusInterval, c.Polling.ProbeInterval, c.Polling.Timeout} {
		if d <= 0 || d > 24*time.Hour {
			return errors.New("polling intervals and timeout must be positive and at most 24h")
		}
	}
	if c.Polling.Timeout > c.Polling.StatusInterval || c.Polling.Timeout > c.Polling.ProbeInterval {
		return errors.New("polling.timeout must not exceed either interval")
	}
	if c.Polling.Jitter < 0 || c.Polling.Jitter > min(c.Polling.StatusInterval, c.Polling.ProbeInterval) {
		return errors.New("polling.jitter must be nonnegative and not exceed either interval")
	}
	if len(c.PSPs) == 0 || len(c.PSPs) > MaxPSPs {
		return fmt.Errorf("psps must contain between 1 and %d entries", MaxPSPs)
	}
	seen := map[string]bool{}
	hasProbes := false
	for i, p := range c.PSPs {
		prefix := fmt.Sprintf("psps[%d]", i)
		if !identifier.MatchString(p.ID) || seen[p.ID] {
			return fmt.Errorf("%s.id must be valid and unique", prefix)
		}
		seen[p.ID] = true
		if len(p.DisplayName) > 256 {
			return fmt.Errorf("%s.display_name is too long", prefix)
		}
		if !validKind(p.Kind) {
			return fmt.Errorf("%s.kind must be one of %v", prefix, Kinds)
		}
		switch p.Status.Type {
		case StatusTypeNone:
			if p.Status.BaseURL != "" || len(p.Status.Headers) > 0 || len(p.Status.Components) > 0 {
				return fmt.Errorf("%s.status: none cannot have a URL, headers or components", prefix)
			}
		case StatusTypeStatuspageV2, StatusTypeInstatusV1, StatusTypeHiPayV1:
			if !validURL(p.Status.BaseURL, true) {
				return fmt.Errorf("%s.status.base_url must be an HTTP(S) URL without credentials, query or fragment", prefix)
			}
		case StatusTypeAdyenV1, StatusTypePayPalV1:
			// These providers publish no component inventory, so a component
			// mapping could never resolve and is rejected rather than ignored.
			if !validURL(p.Status.BaseURL, true) {
				return fmt.Errorf("%s.status.base_url must be an HTTP(S) URL without credentials, query or fragment", prefix)
			}
			if len(p.Status.Components) > 0 {
				return fmt.Errorf("%s.status: this source publishes no components", prefix)
			}
		default:
			return fmt.Errorf("%s.status.type must be statuspage_v2, instatus_v1, hipay_v1, adyen_v1, paypal_v1 or none", prefix)
		}
		if !validHeaders(p.Status.Headers) {
			return fmt.Errorf("%s.status.headers are invalid", prefix)
		}
		if len(p.Status.Components) > MaxComponents {
			return fmt.Errorf("%s.status.components exceeds the limit", prefix)
		}
		componentIDs := map[string]bool{}
		for id, upstream := range p.Status.Components {
			if id == "overall" || !identifier.MatchString(id) || !identifier.MatchString(upstream) || componentIDs[upstream] {
				return fmt.Errorf("%s.status.components must have valid unique IDs; overall is reserved", prefix)
			}
			componentIDs[upstream] = true
		}
		if len(p.Probes) > MaxProbes {
			return fmt.Errorf("%s.probes exceeds the limit", prefix)
		}
		endpoints := map[string]bool{}
		for j, probe := range p.Probes {
			if !identifier.MatchString(probe.ID) || endpoints[probe.ID] {
				return fmt.Errorf("%s.probes[%d].id must be valid and unique within its PSP", prefix, j)
			}
			endpoints[probe.ID] = true
			if !identifier.MatchString(probe.Module) {
				return fmt.Errorf("%s.probes[%d].module is invalid", prefix, j)
			}
			if !validTarget(probe.Target) {
				return fmt.Errorf("%s.probes[%d].target must be an HTTP(S) URL or host:port without credentials or fragment", prefix, j)
			}
			hasProbes = true
		}
	}
	if (hasProbes || c.Blackbox.BaseURL != "") && !validURL(c.Blackbox.BaseURL, true) {
		return errors.New("blackbox.base_url is required for probes and must be an HTTP(S) URL without credentials, query or fragment")
	}
	if !validHeaders(c.Blackbox.Headers) {
		return errors.New("blackbox.headers are invalid")
	}
	return nil
}

func validURL(s string, base bool) bool {
	if len(s) > 4096 || strings.ContainsAny(s, "\r\n\t ") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	if u.Port() != "" && !validPort(u.Port()) {
		return false
	}
	return !base || (u.RawQuery == "" && !u.ForceQuery)
}
func validKind(s string) bool {
	if s == "" {
		return true
	}
	for _, kind := range Kinds {
		if s == kind {
			return true
		}
	}
	return false
}
func validPort(s string) bool { n, err := strconv.Atoi(s); return err == nil && n > 0 && n <= 65535 }
func validTarget(s string) bool {
	if strings.Contains(s, "://") {
		return validURL(s, false)
	}
	if len(s) > 512 || strings.ContainsAny(s, "/\r\n\t @?#") {
		return false
	}
	host, port, err := net.SplitHostPort(s)
	return err == nil && host != "" && validPort(port)
}
func validHeaders(headers map[string]string) bool {
	if len(headers) > 32 {
		return false
	}
	canonical := map[string]bool{}
	for k, v := range headers {
		key := http.CanonicalHeaderKey(k)
		if !headerName.MatchString(k) || canonical[key] || len(v) > 8192 {
			return false
		}
		canonical[key] = true
		switch key {
		case "Host", "Content-Length", "Connection", "Transfer-Encoding", "Upgrade":
			return false
		}
		for _, r := range v {
			if r < 32 || r == 127 {
				return false
			}
		}
	}
	return true
}
