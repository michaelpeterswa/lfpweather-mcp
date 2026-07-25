// Package lfpweather is a thin HTTP client for the lfpweather-api server. It
// wraps the structured query endpoint (POST /api/v1/query) and the field
// discovery endpoint (GET /api/v1/query/fields) that back this MCP server.
package lfpweather

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to a single lfpweather-api instance.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithHTTPClient sets the underlying http.Client (timeout, OTel transport).
func WithHTTPClient(h *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = h
	}
}

// WithAPIKey sets the X-API-Key sent on every request. Empty disables the
// header (for an upstream with authentication turned off).
func WithAPIKey(key string) ClientOption {
	return func(c *Client) {
		c.apiKey = key
	}
}

// NewClient returns a Client for the given base URL (e.g.
// http://lfpweather-api:8080, with no trailing /api/v1).
func NewClient(baseURL string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// QueryRequest is the body of POST /api/v1/query. Fields mirror the upstream
// API; omitempty keeps optional fields out of the request when unset.
type QueryRequest struct {
	Metric       string   `json:"metric"`
	Range        string   `json:"range,omitempty"`
	Start        *string  `json:"start,omitempty"`
	End          *string  `json:"end,omitempty"`
	Bucket       string   `json:"bucket,omitempty"`
	Aggregations []string `json:"aggregations,omitempty"`
	GroupBy      string   `json:"group_by,omitempty"`
}

// QueryPoint is one bucket of a returned series.
type QueryPoint struct {
	Time       time.Time `json:"time"`
	Avg        *float64  `json:"avg,omitempty"`
	Min        *float64  `json:"min,omitempty"`
	Max        *float64  `json:"max,omitempty"`
	First      *float64  `json:"first,omitempty"`
	Last       *float64  `json:"last,omitempty"`
	Count      *int64    `json:"count,omitempty"`
	CommonName string    `json:"common_name,omitempty"`
}

// QueryResponse is the result of a query, including the resolved window/bucket.
type QueryResponse struct {
	Metric  string       `json:"metric"`
	Type    string       `json:"type"`
	Start   time.Time    `json:"start"`
	End     time.Time    `json:"end"`
	Bucket  string       `json:"bucket"`
	GroupBy string       `json:"group_by,omitempty"`
	Points  []QueryPoint `json:"points"`
}

// FieldInfo describes one queryable column.
type FieldInfo struct {
	Name     string `json:"name"`
	DataType string `json:"data_type"`
	Numeric  bool   `json:"numeric"`
}

// TableFields is one table in the discovery response.
type TableFields struct {
	Type    string      `json:"type"`
	Columns []FieldInfo `json:"columns"`
}

// FieldsResponse is the result of GET /api/v1/query/fields.
type FieldsResponse struct {
	Tables  map[string]TableFields `json:"tables"`
	Aliases map[string]string      `json:"aliases"`
}

// Query runs a structured query against the upstream API.
func (c *Client) Query(ctx context.Context, req QueryRequest) (*QueryResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("could not marshal query request: %w", err)
	}

	httpReq, err := c.newRequest(ctx, http.MethodPost, "/api/v1/query", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	var out QueryResponse
	if err := c.do(httpReq, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Fields fetches the discovery catalog of queryable tables and columns.
func (c *Client) Fields(ctx context.Context) (*FieldsResponse, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/api/v1/query/fields", nil)
	if err != nil {
		return nil, err
	}

	var out FieldsResponse
	if err := c.do(httpReq, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("could not create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	return req, nil
}

// do executes a request and decodes a 2xx JSON body into out. Non-2xx
// responses are turned into an error carrying the RFC 9457 problem detail.
func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request to lfpweather-api failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("could not read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("lfpweather-api returned %s: %s", resp.Status, problemDetail(data))
	}

	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("could not decode response: %w", err)
	}
	return nil
}

// problemDetail extracts a human-readable message from an RFC 9457
// problem+json body, falling back to the raw payload.
func problemDetail(data []byte) string {
	var problem struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(data, &problem); err == nil {
		switch {
		case problem.Title != "" && problem.Detail != "":
			return problem.Title + ": " + problem.Detail
		case problem.Detail != "":
			return problem.Detail
		case problem.Title != "":
			return problem.Title
		}
	}

	msg := strings.TrimSpace(string(data))
	if msg == "" {
		return "(empty response body)"
	}
	return msg
}
