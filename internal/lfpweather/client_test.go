package lfpweather

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQuerySendsRequestAndDecodes(t *testing.T) {
	var gotPath, gotMethod, gotAPIKey string
	var gotBody QueryRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAPIKey = r.Header.Get("X-API-Key")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"metric":"temperature","type":"gauge","bucket":"1h","points":[{"time":"2026-07-24T00:00:00Z","avg":21.5}]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("secret-key"))

	resp, err := c.Query(context.Background(), QueryRequest{Metric: "temperature", Range: "24h"})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/v1/query" {
		t.Errorf("path = %s, want /api/v1/query", gotPath)
	}
	if gotAPIKey != "secret-key" {
		t.Errorf("X-API-Key = %q, want secret-key", gotAPIKey)
	}
	if gotBody.Metric != "temperature" || gotBody.Range != "24h" {
		t.Errorf("upstream body = %+v, want metric=temperature range=24h", gotBody)
	}
	if len(resp.Points) != 1 || resp.Points[0].Avg == nil || *resp.Points[0].Avg != 21.5 {
		t.Errorf("decoded points = %+v, want one point with avg 21.5", resp.Points)
	}
}

func TestQuerySurfacesProblemDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"title":"invalid query","detail":"unknown metric \"unicorns\""}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)

	_, err := c.Query(context.Background(), QueryRequest{Metric: "unicorns", Range: "24h"})
	if err == nil {
		t.Fatal("expected an error for a 400 response")
	}
	if got := err.Error(); !strings.Contains(got, "invalid query") || !strings.Contains(got, "unicorns") {
		t.Errorf("error = %q, want it to include the problem title and detail", got)
	}
}

func TestRecordsSendsRequestAndDecodes(t *testing.T) {
	var gotPath, gotQuery, gotMethod, gotAPIKey string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotMethod = r.Method
		gotAPIKey = r.Header.Get("X-API-Key")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"period":"month","complete":true,"records":[{"metric":"temperature","high":{"value":104.1,"time":"2025-07-08T15:12:00-07:00"},"low":{"value":58.3,"time":"2025-07-02T05:41:00-07:00"}},{"metric":"wind_gust","high":{"value":41,"time":"2025-07-07T14:33:00-07:00"}}]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("secret-key"))

	resp, err := c.Records(context.Background(), "month", "2025-07-15")
	if err != nil {
		t.Fatalf("Records() error = %v", err)
	}

	if gotMethod != http.MethodGet {
		t.Errorf("method = %s, want GET", gotMethod)
	}
	if gotPath != "/api/v1/records/month" {
		t.Errorf("path = %s, want /api/v1/records/month", gotPath)
	}
	if !strings.Contains(gotQuery, "at=2025-07-15") {
		t.Errorf("query = %q, want it to include at=2025-07-15", gotQuery)
	}
	if gotAPIKey != "secret-key" {
		t.Errorf("X-API-Key = %q, want secret-key", gotAPIKey)
	}
	if !resp.Complete {
		t.Error("complete = false, want true")
	}
	if len(resp.Records) != 2 {
		t.Fatalf("records = %d, want 2", len(resp.Records))
	}
	if resp.Records[0].High == nil || resp.Records[0].High.Value != 104.1 {
		t.Errorf("temperature high = %+v, want 104.1", resp.Records[0].High)
	}
	if resp.Records[0].Low == nil || resp.Records[0].Low.Value != 58.3 {
		t.Errorf("temperature low = %+v, want 58.3", resp.Records[0].Low)
	}
	// wind_gust is a high-only metric.
	if resp.Records[1].Low != nil {
		t.Errorf("wind_gust low = %+v, want nil", resp.Records[1].Low)
	}
}

func TestRecordsOmitsAtWhenEmpty(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"period":"all","complete":false,"records":[]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if _, err := c.Records(context.Background(), "all", ""); err != nil {
		t.Fatalf("Records() error = %v", err)
	}
	if gotQuery != "" {
		t.Errorf("query = %q, want empty when at is unset", gotQuery)
	}
}

func TestNoAPIKeyHeaderWhenUnset(t *testing.T) {
	var hadHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadHeader = r.Header["X-Api-Key"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tables":{},"aliases":{}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if _, err := c.Fields(context.Background()); err != nil {
		t.Fatalf("Fields() error = %v", err)
	}
	if hadHeader {
		t.Error("X-API-Key header should be absent when no key is configured")
	}
}
