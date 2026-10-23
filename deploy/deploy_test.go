// Package deploy_test keeps the Helm chart and the kustomize base identical.
// They describe the same deployment twice, and a second copy that drifts is
// how the committed stack once stopped matching the deployed one.
package deploy_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// requireTools turns a missing helm or kustomize into a failure instead of a
// skip. CI sets it, so the comparison cannot silently stop running there.
const requireTools = "PSSST_REQUIRE_DEPLOY_TOOLS"

func TestChartFilesMatchSources(t *testing.T) {
	for chart, source := range map[string]string{
		"helm/pssst/files/pssst.psp.yml": "pssst.psp.yml",
		"helm/pssst/files/blackbox.yml":  "blackbox/blackbox.yml",
	} {
		a, err := os.ReadFile(chart)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s differs from %s: run make chart-sync", chart, source)
		}
	}
}

func TestChartRendersTheKustomizeBase(t *testing.T) {
	helm := tool(t, "helm")
	var kustomize []string
	if path, err := exec.LookPath("kustomize"); err == nil {
		kustomize = []string{path, "build", "."}
	} else {
		kustomize = []string{tool(t, "kubectl"), "kustomize", "."}
	}

	base := render(t, kustomize...)
	// The base ships its ServiceMonitor; the chart makes it opt-in.
	chart := render(t, helm, "template", "pssst", "helm/pssst", "--set", "serviceMonitor.enabled=true")

	if a, b := keys(base), keys(chart); !slices.Equal(a, b) {
		t.Fatalf("objects differ:\nkustomize %v\nhelm      %v", a, b)
	}
	honorsLabels(t, "kustomize", base["ServiceMonitor/pssst"])
	honorsLabels(t, "helm", chart["ServiceMonitor/pssst"])
	for key, want := range base {
		if got := chart[key]; !reflect.DeepEqual(got, want) {
			w, _ := json.MarshalIndent(want, "", "  ")
			g, _ := json.MarshalIndent(got, "", "  ")
			t.Errorf("%s differs:\nkustomize %s\nhelm      %s", key, w, g)
		}
	}
}

// honorsLabels fails unless every endpoint of a rendered ServiceMonitor keeps
// the exporter's labels. Otherwise the Operator's endpoint="metrics" renames
// each probe's endpoint label, and the rules' per-endpoint freshness joins let
// a fresh probe vouch for a stale one.
func honorsLabels(t *testing.T, renderer string, monitor any) {
	t.Helper()
	object, _ := monitor.(map[string]any)
	spec, _ := object["spec"].(map[string]any)
	endpoints, _ := spec["endpoints"].([]any)
	if len(endpoints) == 0 {
		t.Fatalf("%s: ServiceMonitor/pssst is missing or has no endpoints", renderer)
	}
	for i, endpoint := range endpoints {
		if e, _ := endpoint.(map[string]any); e["honorLabels"] != true {
			t.Errorf("%s: ServiceMonitor/pssst endpoint %d does not set honorLabels: true", renderer, i)
		}
	}
}

func tool(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err == nil {
		return path
	}
	if os.Getenv(requireTools) != "" {
		t.Fatalf("%s not found and %s is set", name, requireTools)
	}
	t.Skipf("%s not found; set %s to make this a failure", name, requireTools)
	return ""
}

// hashed matches the content hash kustomize appends to generated ConfigMaps;
// the chart rolls its pods with a checksum annotation instead.
var hashed = regexp.MustCompile(`^(pssst-config|pssst-blackbox-config)-[a-z0-9]{10}$`)

// helmOnly are the labels Helm adds for its own bookkeeping. They sit outside
// every selector, so they never change which pods a Service or monitor picks.
var helmOnly = []string{
	"helm.sh/chart",
	"app.kubernetes.io/managed-by",
	"app.kubernetes.io/instance",
	"app.kubernetes.io/version",
}

// render runs a renderer and returns its objects keyed by kind/name, each
// reduced to what must match: the spec, or a ConfigMap's data, and the pod
// template's labels and annotations.
func render(t *testing.T, args ...string) map[string]any {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	objects := map[string]any{}
	decoder := yaml.NewDecoder(bytes.NewReader(out))
	for {
		var doc map[string]any
		if err := decoder.Decode(&doc); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if doc == nil {
			continue
		}
		normalize(doc)
		meta := doc["metadata"].(map[string]any)
		key := doc["kind"].(string) + "/" + meta["name"].(string)
		reduced := map[string]any{"metadata.labels": stripHelm(meta["labels"])}
		for _, field := range []string{"spec", "data"} {
			if v, ok := doc[field]; ok {
				reduced[field] = v
			}
		}
		objects[key] = reduced
	}
	return objects
}

// normalize rewrites, in place, what legitimately differs between the two
// renderers: hashed ConfigMap names, checksum annotations, Helm's labels, and
// the formatting of JSON held in annotations.
func normalize(node any) {
	switch n := node.(type) {
	case map[string]any:
		for k, v := range n {
			if s, ok := v.(string); ok && hashed.MatchString(s) {
				n[k] = hashed.FindStringSubmatch(s)[1]
				continue
			}
			if k == "annotations" {
				if a, ok := v.(map[string]any); ok {
					for name, value := range a {
						if strings.HasPrefix(name, "checksum/") {
							delete(a, name)
							continue
						}
						var parsed any
						if s, ok := value.(string); ok && json.Unmarshal([]byte(s), &parsed) == nil {
							a[name] = parsed
						}
					}
				}
			}
			if k == "labels" {
				n[k] = stripHelm(v)
			}
			normalize(n[k])
		}
	case []any:
		for _, v := range n {
			normalize(v)
		}
	}
}

func stripHelm(labels any) any {
	m, ok := labels.(map[string]any)
	if !ok {
		return labels
	}
	for _, name := range helmOnly {
		delete(m, name)
	}
	return m
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
