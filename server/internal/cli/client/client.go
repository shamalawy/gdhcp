// Package client is a thin typed REST client for the gdhcp API
// (docs/api/endpoints.md), used by the gdhcp CLI (docs/AMENDMENTS.md A2).
// Every method issues one HTTP request against a gdhcp server's
// /api/v1 surface and decodes either the expected response shape or, on
// failure, the standard error envelope (docs/CONTRACTS.md Part 6) into an
// *APIError with a friendly Error() message.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to one gdhcp server's REST API over HTTP(S).
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New constructs a Client for server (host:port or a full URL; a bare
// host:port is assumed to be plain HTTP) authenticating requests with
// token, if non-empty, as an Authorization: Bearer header (docs/api/
// endpoints.md — accepts either a session JWT from `gdhcp login` or a
// long-lived API token).
func New(server, token string) *Client {
	base := normalizeServer(server)
	return &Client{
		baseURL: base,
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// normalizeServer ensures server carries a scheme and has no trailing
// slash, so callers can pass "localhost:8080", "http://localhost:8080", or
// "https://dhcp.example.com" interchangeably.
func normalizeServer(server string) string {
	server = strings.TrimSpace(server)
	server = strings.TrimSuffix(server, "/")
	if !strings.Contains(server, "://") {
		server = "http://" + server
	}
	return server
}

// Page is the pagination envelope wrapping list responses
// (docs/CONTRACTS.md Part 6 "List responses").
type Page[T any] struct {
	Items  []T   `json:"items"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

// ListParams carries the common list query parameters (docs/CONTRACTS.md
// Part 6 "Common Query Parameters"). Zero values are omitted from the
// request so server-side defaults apply.
type ListParams struct {
	Limit  int
	Offset int
	Sort   string
	Search string
	Extra  url.Values // resource-specific filters (vrf_id, scope_id, state, ...)
}

// Values renders p as URL query parameters.
func (p ListParams) Values() url.Values {
	v := url.Values{}
	for key, vals := range p.Extra {
		for _, val := range vals {
			if val != "" {
				v.Add(key, val)
			}
		}
	}
	if p.Limit > 0 {
		v.Set("limit", fmt.Sprintf("%d", p.Limit))
	}
	if p.Offset > 0 {
		v.Set("offset", fmt.Sprintf("%d", p.Offset))
	}
	if p.Sort != "" {
		v.Set("sort", p.Sort)
	}
	if p.Search != "" {
		v.Set("search", p.Search)
	}
	return v
}

// errorEnvelope mirrors docs/CONTRACTS.md Part 6's "Error Envelope (All
// Errors)" JSON shape.
type errorEnvelope struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
		TraceID string         `json:"trace_id"`
	} `json:"error"`
}

// APIError represents a decoded error envelope from the gdhcp API, or a
// transport-level failure to reach it. It is always what request methods
// return on failure so callers (and the CLI's top-level error printer) can
// rely on a consistent, human-readable Error() string.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	Details    map[string]any
	TraceID    string
}

// Error implements error, rendering a friendly single-line message.
func (e *APIError) Error() string {
	var b strings.Builder
	if e.Code != "" {
		fmt.Fprintf(&b, "%s: ", e.Code)
	}
	if e.Message != "" {
		b.WriteString(e.Message)
	} else {
		fmt.Fprintf(&b, "request failed with status %d", e.StatusCode)
	}
	if len(e.Details) > 0 {
		fmt.Fprintf(&b, " (details: %v)", e.Details)
	}
	if e.TraceID != "" {
		fmt.Fprintf(&b, " [trace: %s]", e.TraceID)
	}
	return b.String()
}

// IsNotFound reports whether err is an APIError for a 404 response —
// callers use this to distinguish "doesn't exist" from other failures
// without string-matching the message.
func IsNotFound(err error) bool {
	apiErr, ok := err.(*APIError)
	return ok && apiErr.StatusCode == http.StatusNotFound
}

// request issues one HTTP call to path (already including /api/v1 unless
// raw is true) with optional query values and a JSON-encodable body, and
// decodes a successful (2xx) response into out (skipped if out is nil).
// On a non-2xx response it decodes the error envelope into an *APIError; if
// the body isn't a valid envelope (e.g. a proxy's plain-text 502), the raw
// body becomes the message so the failure is still legible.
func (c *Client) request(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("client: encode request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return fmt.Errorf("client: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("client: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("client: read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeAPIError(resp.StatusCode, respBody)
	}

	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("client: decode response: %w", err)
	}
	return nil
}

func decodeAPIError(status int, body []byte) *APIError {
	var env errorEnvelope
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Message != "" {
		return &APIError{
			StatusCode: status,
			Code:       env.Error.Code,
			Message:    env.Error.Message,
			Details:    env.Error.Details,
			TraceID:    env.Error.TraceID,
		}
	}
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = http.StatusText(status)
	}
	return &APIError{StatusCode: status, Message: msg}
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.request(ctx, http.MethodGet, path, query, nil, out)
}

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	return c.request(ctx, http.MethodPost, path, nil, body, out)
}

func (c *Client) put(ctx context.Context, path string, body any, out any) error {
	return c.request(ctx, http.MethodPut, path, nil, body, out)
}

func (c *Client) delete(ctx context.Context, path string) error {
	return c.request(ctx, http.MethodDelete, path, nil, nil, nil)
}

// messageResponse decodes the common `{"message": "..."}` response shape
// returned by delete/action endpoints (docs/api/endpoints.md).
type messageResponse struct {
	Message string `json:"message"`
}
