package statuspage

import "testing"

func TestNormalizeSeverity(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"none":     "none",
		"minor":    "minor",
		"major":    "major",
		"critical": "critical",
		"NONE":     "none",
		"Minor":    "minor",
		"MAJOR":    "major",
		"Critical": "critical",
		"":         "unknown",
		" minor":   "unknown", // not trimmed, unlike the Adyen and PayPal readers
		"minor ":   "unknown",
		"severe":   "unknown",
		"mäjor":    "unknown",
	} {
		if got := normalizeSeverity(in); got != want {
			t.Errorf("normalizeSeverity(%q) = %q, want %q", in, got, want)
		}
	}
}
