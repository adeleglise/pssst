package blackbox

import "context"

type ProbeProvider interface {
	Probe(ctx context.Context, module, target string) (Result, error)
}

type Result struct {
	Success            bool
	DurationSeconds    float64
	HTTPStatusCode     *float64
	EarliestCertExpiry *float64
}
