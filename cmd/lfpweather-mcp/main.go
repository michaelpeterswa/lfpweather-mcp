package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// get_current_time resolves America/Los_Angeles at runtime. Embedding the
	// tz database keeps that working on distroless and scratch images, which
	// ship no /usr/share/zoneinfo.
	_ "time/tzdata"

	"alpineworks.io/ootel"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/michaelpeterswa/lfpweather-mcp/internal/config"
	"github.com/michaelpeterswa/lfpweather-mcp/internal/handlers"
	"github.com/michaelpeterswa/lfpweather-mcp/internal/lfpweather"
	"github.com/michaelpeterswa/lfpweather-mcp/internal/logging"
	"github.com/michaelpeterswa/lfpweather-mcp/internal/mcpserver"
	"go.opentelemetry.io/contrib/instrumentation/host"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
)

func main() {
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "error"
	}

	slogLevel, err := logging.LogLevelToSlogLevel(logLevel)
	if err != nil {
		log.Fatalf("could not convert log level: %s", err)
	}

	// Logs go to stderr, not stdout: the stdio transport speaks JSON-RPC over
	// stdout and any log line written there corrupts the stream. Docker and
	// Kubernetes collect both streams identically.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slogLevel,
	})))

	c, err := config.NewConfig()
	if err != nil {
		slog.Error("could not create config", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Kubernetes sends SIGTERM and then waits out terminationGracePeriodSeconds
	// before SIGKILL; cancelling ctx here is what lets in-flight tool calls
	// finish during a rolling update.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	exporterType := ootel.ExporterTypePrometheus
	if c.Local {
		exporterType = ootel.ExporterTypeOTLPGRPC
	}

	ootelClient := ootel.NewOotelClient(
		ootel.WithMetricConfig(
			ootel.NewMetricConfig(
				c.MetricsEnabled,
				exporterType,
				c.MetricsPort,
			),
		),
		ootel.WithTraceConfig(
			ootel.NewTraceConfig(
				c.TracingEnabled,
				c.TracingSampleRate,
				c.TracingService,
				c.TracingVersion,
			),
		),
	)

	shutdown, err := ootelClient.Init(ctx)
	if err != nil {
		slog.Error("could not create ootel client", slog.String("error", err.Error()))
		os.Exit(1)
	}

	err = runtime.Start(runtime.WithMinimumReadMemStatsInterval(5 * time.Second))
	if err != nil {
		slog.Error("could not create runtime metrics", slog.String("error", err.Error()))
		os.Exit(1)
	}

	err = host.Start()
	if err != nil {
		slog.Error("could not create host metrics", slog.String("error", err.Error()))
		os.Exit(1)
	}

	defer func() {
		_ = shutdown(ctx)
	}()

	httpClient := http.Client{
		Timeout:   c.LFPWeatherTimeout,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}

	weatherClient := lfpweather.NewClient(
		c.LFPWeatherBaseURL,
		lfpweather.WithHTTPClient(&httpClient),
		lfpweather.WithAPIKey(c.LFPWeatherAPIKey),
	)

	whc := handlers.NewWeatherHandlerClient(weatherClient)

	// Every tool only reads upstream data, so annotate read-only and
	// non-destructive. The weather tools reach an external API (open world);
	// get_current_time is a local clock (closed world). Descriptions include
	// the words people actually use (weather, temperature, air quality, birds,
	// battery, solar) so the tools are discoverable across phrasings.
	tools := []mcpserver.Tool{
		mcpserver.NewTool(
			mcp.NewTool(
				"query_weather",
				mcp.WithTitleAnnotation("Query a weather/environment metric over time"),
				mcp.WithDescription(
					"Query a time series for one environment metric from the Lake "+
						"Forest Park station: weather (temperature, humidity, pressure, "+
						"wind_speed, wind_gust, rain_rate, solar_radiation, uv_index), air "+
						"quality (aqi, co2, nox_index, tvoc_index), bird detections "+
						"(birdnet), battery (litime.*) and solar charge controller "+
						"(renogychargecontroller.*). Use it for history, trends, "+
						"averages, minima/maxima, and \"over the last week/month/year\" "+
						"questions. Pass a friendly name, or a table.column reference "+
						"(call list_weather_fields to discover columns). Unless you set a "+
						"bucket, it is sized to return about 50 points, enough for "+
						"trends and extremes; set a finer bucket only when the "+
						"question needs that detail.",
				),
				mcp.WithString("metric",
					mcp.Description("Metric to query: a friendly alias (temperature, humidity, pressure, wind_speed, wind_gust, rain_rate, solar_radiation, uv_index, aqi, co2, nox_index, tvoc_index), the count metric 'birdnet', or a table.column reference such as renogychargecontroller.battery_voltage or litime.soc."),
					mcp.Required(),
				),
				mcp.WithString("range",
					mcp.Description("Named time range relative to now. Mutually exclusive with start/end. Defaults to 24h."),
					mcp.Enum("12h", "24h", "7d", "30d", "90d", "ytd", "last_12mo", "prev_year", "all"),
				),
				mcp.WithString("start",
					mcp.Description("Absolute start (RFC3339, e.g. 2026-01-01T00:00:00Z). Use with end instead of range."),
				),
				mcp.WithString("end",
					mcp.Description("Absolute end (RFC3339). Optional; defaults to now when start is given."),
				),
				mcp.WithString("bucket",
					mcp.Description("Override the time bucket. Leave unset to get about 50 points across the range."),
					mcp.Enum("5m", "15m", "30m", "1h", "3h", "6h", "12h", "1d", "1w"),
				),
				mcp.WithArray("aggregations",
					mcp.Description("Which statistics to return per bucket for gauge metrics. Defaults to avg, min, max."),
					mcp.WithStringEnumItems([]string{"avg", "min", "max", "count", "first", "last"}),
				),
				mcp.WithString("group_by",
					mcp.Description("Only for the birdnet count metric: group counts by species."),
					mcp.Enum("common_name"),
				),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(true),
			),
			whc.QueryWeatherHandler,
		),
		mcpserver.NewTool(
			mcp.NewTool(
				"list_weather_fields",
				mcp.WithTitleAnnotation("List queryable metrics and columns"),
				mcp.WithDescription(
					"List every table, column, and type that query_weather can "+
						"query, plus the friendly metric aliases. Call this to discover "+
						"what data exists — for example the exact battery or solar "+
						"charge-controller columns — before building a query_weather call.",
				),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(true),
			),
			whc.ListWeatherFieldsHandler,
		),
		mcpserver.NewTool(
			mcp.NewTool(
				"get_weather_latest",
				mcp.WithTitleAnnotation("Latest reading for metrics"),
				mcp.WithDescription(
					"Get the most recent reading for one or more metrics — the "+
						"current conditions. Answers \"what's the temperature/humidity/"+
						"AQI/CO2 right now\", \"current battery voltage\", or \"how much "+
						"solar power right now\". Accepts friendly aliases or table.column "+
						"references (see list_weather_fields).",
				),
				mcp.WithArray("metrics",
					mcp.Description("Metrics to fetch the latest value for (friendly aliases or table.column references)."),
					mcp.WithStringItems(),
					mcp.Required(),
				),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(true),
			),
			whc.GetWeatherLatestHandler,
		),
		mcpserver.NewTool(
			mcp.NewTool(
				"get_bird_detections",
				mcp.WithTitleAnnotation("Bird species detected"),
				mcp.WithDescription(
					"List birds detected acoustically at the station (BirdNET), with "+
						"a detection count per species over a time range, most-detected "+
						"first. Answers \"what birds have been around\", \"which birds "+
						"today/this week\". Defaults to the last 24 hours.",
				),
				mcp.WithString("range",
					mcp.Description("Named time range to summarize over. Defaults to 24h."),
					mcp.Enum("12h", "24h", "7d", "30d", "90d", "ytd", "last_12mo", "prev_year", "all"),
				),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(true),
			),
			whc.GetBirdDetectionsHandler,
		),
		mcpserver.NewTool(
			mcp.NewTool(
				"get_current_time",
				mcp.WithTitleAnnotation("Current Pacific time"),
				mcp.WithDescription(
					"Get the current date and time in US Pacific time "+
						"(America/Los_Angeles, PST/PDT) — the timezone the station "+
						"reports in. Use it to reason about \"now\", \"today\", or "+
						"\"tonight\" relative to the data.",
				),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(false),
			),
			handlers.CurrentTimeHandler,
		),
		mcpserver.NewTool(
			mcp.NewTool(
				"get_et0",
				mcp.WithTitleAnnotation("Reference evapotranspiration (ET0)"),
				mcp.WithDescription(
					"Get the year-to-date reference evapotranspiration (ET0, FAO-56 "+
						"Penman-Monteith) for the station, in millimeters. Answers "+
						"\"how much water have plants lost\", \"evapotranspiration this "+
						"year\", and irrigation-demand questions.",
				),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(true),
			),
			whc.GetET0Handler,
		),
		mcpserver.NewTool(
			mcp.NewTool(
				"get_gdd",
				mcp.WithTitleAnnotation("Growing degree days (GDD)"),
				mcp.WithDescription(
					"Get the year-to-date growing degree days (GDD) for the station. "+
						"Answers \"how much growing heat has accumulated\", \"degree days "+
						"this season\", and crop or pest development questions.",
				),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(true),
			),
			whc.GetGDDHandler,
		),
		mcpserver.NewTool(
			mcp.NewTool(
				"get_barometric_forecast",
				mcp.WithTitleAnnotation("Barometric (Zambretti) forecast"),
				mcp.WithDescription(
					"Get the current short-term weather forecast derived from "+
						"barometric pressure and its trend (the Zambretti forecaster). "+
						"Answers \"what does the barometer say\", \"is fair or stormy "+
						"weather coming\", and pressure-based forecast questions.",
				),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithOpenWorldHintAnnotation(true),
			),
			whc.GetBarometricForecastHandler,
		),
	}

	// Server-level instructions tell client/agent routers what this whole
	// server is for, so it surfaces for weather/environment prompts.
	instructions := "Lake Forest Park (LFP) home weather and environment station. " +
		"Serves live and historical readings: weather (temperature, humidity, " +
		"barometric pressure, wind, rainfall, solar radiation, UV), air quality " +
		"(AQI, CO2, NOx, TVOC), fire-danger indices (ERC, Burning Index, KBDI, " +
		"GSI), acoustic bird detections (BirdNET), and the off-grid power system " +
		"(Victron solar charger and battery monitor, LiTime battery). Use " +
		"get_weather_latest for current conditions, query_weather for history " +
		"and trends over ranges up to multiple years (buckets auto-size to the " +
		"range), list_weather_fields to discover the exact tables/columns " +
		"available, get_bird_detections for birds, get_et0 and get_gdd for " +
		"reference evapotranspiration and growing degree days, " +
		"get_barometric_forecast for the pressure-based (Zambretti) forecast, " +
		"and get_current_time for the Pacific time the station reports in."

	err = mcpserver.StartServer(ctx, c,
		mcpserver.WithServerOptions(server.WithInstructions(instructions)),
		mcpserver.WithTools(tools),
	)
	if err != nil {
		slog.Error("could not start server", slog.String("error", err.Error()))
		os.Exit(1)
	}
}
