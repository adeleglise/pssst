// Package httpclient provides bounded HTTP retrieval for upstream adapters.
package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

const MaxBodyBytes int64 = 2 << 20

// Client makes bounded HTTP GET requests. Errors intentionally contain only a
// small local error class, never a target URL, headers, or transport details.
type Client struct {
	client  *http.Client
	headers http.Header
	timeout time.Duration
}

func New(timeout time.Duration, headers map[string]string) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	headerCopy := make(http.Header, len(headers))
	for name, value := range headers {
		headerCopy.Set(name, value)
	}

	dialTimeout := timeout
	if dialTimeout > 5*time.Second {
		dialTimeout = 5 * time.Second
	}
	transport := &http.Transport{
		Proxy:                  http.ProxyFromEnvironment,
		DialContext:            (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           16,
		MaxIdleConnsPerHost:    4,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    dialTimeout,
		ResponseHeaderTimeout:  timeout,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	}
	return &Client{
		client: &http.Client{
			Transport: transport,
			Timeout:   timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		headers: headerCopy,
		timeout: timeout,
	}
}

// Get returns a successful (2xx) response body. Redirects and response bodies
// above MaxBodyBytes are rejected.
func (c *Client) Get(ctx context.Context, endpoint string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, contextError(err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("invalid HTTP request")
	}
	for name, values := range c.headers {
		req.Header[name] = append([]string(nil), values...)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		if requestCtx.Err() != nil {
			return nil, contextError(requestCtx.Err())
		}
		return nil, errors.New("HTTP request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
		return nil, errors.New("HTTP redirect rejected")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("HTTP response status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		if requestCtx.Err() != nil {
			return nil, contextError(requestCtx.Err())
		}
		return nil, errors.New("HTTP response body read failed")
	}
	if int64(len(body)) > MaxBodyBytes {
		return nil, errors.New("HTTP response body too large")
	}
	return body, nil
}

func contextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("HTTP request timed out")
	}
	return errors.New("HTTP request cancelled")
}
