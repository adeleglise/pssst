// Package blackbox retrieves normalized observations from Blackbox Exporter.
package blackbox

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/prometheus/common/expfmt"
	commonmodel "github.com/prometheus/common/model"

	"gitea.doudnas.home.al1.io/alain/pssst/internal/httpclient"
)

const (
	metricSuccess    = "probe_success"
	metricDuration   = "probe_duration_seconds"
	metricHTTPStatus = "probe_http_status_code"
	metricCertExpiry = "probe_ssl_earliest_cert_expiry"
)

// Client queries one Blackbox Exporter /probe response at a time.
type Client struct {
	baseURL string
	baseOK  bool
	http    *httpclient.Client
}

func New(baseURL string, headers map[string]string, timeout time.Duration) *Client {
	_, err := probeEndpoint(baseURL, "", "")
	return &Client{
		baseURL: baseURL,
		baseOK:  err == nil,
		http:    httpclient.New(timeout, headers),
	}
}

func (c *Client) Probe(ctx context.Context, module, target string) (Result, error) {
	if !c.baseOK {
		return Result{}, errors.New("invalid blackbox endpoint")
	}
	endpoint, err := probeEndpoint(c.baseURL, module, target)
	if err != nil {
		return Result{}, errors.New("invalid blackbox request")
	}
	body, err := c.http.Get(ctx, endpoint)
	if err != nil {
		return Result{}, err
	}
	return decodeMetrics(body)
}

func probeEndpoint(baseURL, module, target string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("invalid endpoint")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/probe"
	u.RawPath = ""
	u.Fragment = ""
	query := url.Values{}
	query.Set("module", module)
	query.Set("target", target)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func decodeMetrics(body []byte) (Result, error) {
	parser := expfmt.NewTextParser(commonmodel.LegacyValidation)
	families, err := parser.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		return Result{}, errors.New("invalid blackbox metrics")
	}

	scalar := func(name string) (float64, bool, error) {
		family, found := families[name]
		if !found {
			return 0, false, nil
		}
		if family.GetType().String() != "GAUGE" && family.GetType().String() != "UNTYPED" || len(family.GetMetric()) != 1 {
			return 0, true, errors.New("not a scalar")
		}
		metric := family.GetMetric()[0]
		if len(metric.GetLabel()) != 0 {
			return 0, true, errors.New("labelled scalar")
		}
		var value float64
		switch family.GetType().String() {
		case "GAUGE":
			if metric.GetGauge() == nil {
				return 0, true, errors.New("missing gauge")
			}
			value = metric.GetGauge().GetValue()
		case "UNTYPED":
			if metric.GetUntyped() == nil {
				return 0, true, errors.New("missing untyped value")
			}
			value = metric.GetUntyped().GetValue()
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, true, errors.New("non-finite scalar")
		}
		return value, true, nil
	}

	success, present, err := scalar(metricSuccess)
	if err != nil || !present || (success != 0 && success != 1) {
		return Result{}, errors.New("invalid blackbox metrics")
	}
	duration, present, err := scalar(metricDuration)
	if err != nil || !present || duration < 0 {
		return Result{}, errors.New("invalid blackbox metrics")
	}

	result := Result{Success: success == 1, DurationSeconds: duration}
	if httpStatus, present, err := scalar(metricHTTPStatus); err != nil || (present && (!wholeNonNegative(httpStatus) || (httpStatus != 0 && (httpStatus < 100 || httpStatus > 599)))) {
		return Result{}, errors.New("invalid blackbox metrics")
	} else if present {
		result.HTTPStatusCode = &httpStatus
	}
	if certExpiry, present, err := scalar(metricCertExpiry); err != nil || (present && certExpiry < 0) {
		return Result{}, errors.New("invalid blackbox metrics")
	} else if present {
		result.EarliestCertExpiry = &certExpiry
	}
	return result, nil
}

func wholeNonNegative(value float64) bool {
	return value >= 0 && math.Trunc(value) == value
}
