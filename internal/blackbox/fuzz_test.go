package blackbox

import (
	"math"
	"testing"
)

// FuzzDecodeMetrics feeds arbitrary exposition text to the decoder. Whatever
// the remote sends, the result is an error or a bounded observation.
func FuzzDecodeMetrics(f *testing.F) {
	for _, seed := range []string{
		"# TYPE probe_success gauge\nprobe_success 1\n# TYPE probe_duration_seconds gauge\nprobe_duration_seconds 0.25\n",
		"probe_success 0\nprobe_duration_seconds 2.5\nprobe_http_status_code 503\nprobe_ssl_earliest_cert_expiry 1795000000\n",
		"# TYPE probe_success gauge\nprobe_success 1\n# TYPE probe_duration_seconds gauge\nprobe_duration_seconds 1\n# TYPE ignored_metric gauge\nignored_metric{unbounded=\"x\"} 123\n",
		"probe_success 1\nprobe_duration_seconds NaN\n",
		"probe_success 2\nprobe_duration_seconds 1\n",
		"probe_success{a=\"b\"} 1\nprobe_duration_seconds 1\n",
		"probe_success 1\nprobe_duration_seconds 1\nprobe_http_status_code 99\n",
		"probe_success 1\nprobe_duration_seconds +Inf\n",
		"",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		result, err := decodeMetrics(body)
		if err != nil {
			return
		}
		if math.IsNaN(result.DurationSeconds) || math.IsInf(result.DurationSeconds, 0) || result.DurationSeconds < 0 {
			t.Fatalf("duration %v is not finite and non-negative", result.DurationSeconds)
		}
		if code := result.HTTPStatusCode; code != nil && *code != 0 && (*code < 100 || *code > 599 || math.Trunc(*code) != *code) {
			t.Fatalf("HTTP status %v is neither 0 nor 100-599", *code)
		}
		if expiry := result.EarliestCertExpiry; expiry != nil && (math.IsNaN(*expiry) || math.IsInf(*expiry, 0) || *expiry < 0) {
			t.Fatalf("certificate expiry %v is not finite and non-negative", *expiry)
		}
	})
}
