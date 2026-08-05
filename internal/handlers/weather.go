package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/michaelpeterswa/lfpweather-mcp/internal/lfpweather"
)

// WeatherHandlerClient serves the MCP tools backed by the lfpweather-api client.
type WeatherHandlerClient struct {
	client *lfpweather.Client
}

func NewWeatherHandlerClient(client *lfpweather.Client) *WeatherHandlerClient {
	return &WeatherHandlerClient{client: client}
}

// QueryWeatherHandler backs query_weather: a structured query over any metric.
func (h *WeatherHandlerClient) QueryWeatherHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	metric := mcp.ParseString(request, "metric", "")
	if metric == "" {
		return mcp.NewToolResultError("metric is required"), nil
	}

	req := lfpweather.QueryRequest{
		Metric:       metric,
		Range:        mcp.ParseString(request, "range", ""),
		Bucket:       mcp.ParseString(request, "bucket", ""),
		GroupBy:      mcp.ParseString(request, "group_by", ""),
		Aggregations: parseStringSlice(request, "aggregations"),
	}
	if start := mcp.ParseString(request, "start", ""); start != "" {
		req.Start = &start
	}
	if end := mcp.ParseString(request, "end", ""); end != "" {
		req.End = &end
	}

	resp, err := h.client.Query(ctx, req)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("query failed", err), nil
	}

	return jsonResult(resp)
}

// ListWeatherFieldsHandler backs list_weather_fields: the discovery catalog.
func (h *WeatherHandlerClient) ListWeatherFieldsHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	fields, err := h.client.Fields(ctx)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("could not list fields", err), nil
	}

	return jsonResult(fields)
}

// latestReading is one entry of get_weather_latest's result.
type latestReading struct {
	Metric string   `json:"metric"`
	Time   *string  `json:"time,omitempty"`
	Value  *float64 `json:"value,omitempty"`
	Error  string   `json:"error,omitempty"`
}

// GetWeatherLatestHandler backs get_weather_latest: the most recent reading for
// each requested metric. Implemented over the query endpoint (last aggregation
// in a coarse bucket) so it works for any metric, including the power tables.
func (h *WeatherHandlerClient) GetWeatherLatestHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	metrics := parseStringSlice(request, "metrics")
	if len(metrics) == 0 {
		return mcp.NewToolResultError("metrics is required (a list of metric names or table.column references)"), nil
	}

	out := make([]latestReading, 0, len(metrics))
	for _, metric := range metrics {
		resp, err := h.client.Query(ctx, lfpweather.QueryRequest{
			Metric:       metric,
			Range:        "7d",
			Bucket:       "1d",
			Aggregations: []string{"last"},
		})
		if err != nil {
			out = append(out, latestReading{Metric: metric, Error: err.Error()})
			continue
		}
		if len(resp.Points) == 0 {
			out = append(out, latestReading{Metric: metric, Error: "no readings in the last 7 days"})
			continue
		}

		// Points are ordered by time; the last non-empty bucket is the newest.
		p := resp.Points[len(resp.Points)-1]
		ts := p.Time.Format("2006-01-02T15:04:05Z07:00")
		out = append(out, latestReading{Metric: metric, Time: &ts, Value: p.Last})
	}

	return jsonResult(out)
}

// speciesCount is one entry of get_bird_detections' result.
type speciesCount struct {
	CommonName string `json:"common_name"`
	Count      int64  `json:"count"`
}

// GetBirdDetectionsHandler backs get_bird_detections: species detection totals
// over a range. Counts are summed across buckets so the result is a clean
// per-species total regardless of bucketing.
func (h *WeatherHandlerClient) GetBirdDetectionsHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rng := mcp.ParseString(request, "range", "24h")

	resp, err := h.client.Query(ctx, lfpweather.QueryRequest{
		Metric:  "birdnet",
		Range:   rng,
		Bucket:  "1w", // coarse: we sum across buckets, this just bounds row count
		GroupBy: "common_name",
	})
	if err != nil {
		return mcp.NewToolResultErrorFromErr("could not get bird detections", err), nil
	}

	totals := map[string]int64{}
	for _, p := range resp.Points {
		if p.Count != nil {
			totals[p.CommonName] += *p.Count
		}
	}

	out := make([]speciesCount, 0, len(totals))
	for name, count := range totals {
		out = append(out, speciesCount{CommonName: name, Count: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })

	return jsonResult(out)
}

// GetET0Handler backs get_et0: year-to-date reference evapotranspiration.
func (h *WeatherHandlerClient) GetET0Handler(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp, err := h.client.Meteo(ctx, "/api/v1/et0")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("could not get evapotranspiration", err), nil
	}
	return jsonResult(resp)
}

// GetGDDHandler backs get_gdd: year-to-date growing degree days.
func (h *WeatherHandlerClient) GetGDDHandler(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp, err := h.client.Meteo(ctx, "/api/v1/gdd")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("could not get growing degree days", err), nil
	}
	return jsonResult(resp)
}

// GetBarometricForecastHandler backs get_barometric_forecast: the current
// Zambretti forecast derived from barometric pressure and its trend.
func (h *WeatherHandlerClient) GetBarometricForecastHandler(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp, err := h.client.Meteo(ctx, "/api/v1/zambretti")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("could not get barometric forecast", err), nil
	}
	return jsonResult(resp)
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("could not marshal result: %w", err)
	}
	return mcp.NewToolResultText(string(b)), nil
}

// parseStringSlice reads a JSON array argument as a []string, dropping any
// non-string elements. Returns nil when the argument is absent.
func parseStringSlice(request mcp.CallToolRequest, key string) []string {
	raw := mcp.ParseArgument(request, key, nil)
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
