// Package tracing wires OpenTelemetry tracing for boomtime (TALOS-kvg1).
//
// Design notes:
//   - Tracing is OPT-IN: if OTEL_EXPORTER_OTLP_ENDPOINT is unset, Setup is a
//     no-op returning a no-op shutdown. Dev/CI and the CLI subcommands are
//     therefore completely unaffected.
//   - Export goes to the in-cluster Alloy collector over OTLP/HTTP. Alloy
//     already fans traces to BOTH Tempo and ClickStack/HyperDX, so no
//     pipeline work is needed on the cluster side.
//   - The official otelecho contrib middleware targets echo/v4 and boomtime
//     is on echo/v5, so the HTTP middleware is hand-rolled (middleware.go).
package tracing

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// ServiceName is the logical service the spans are attributed to. Overridable
// with OTEL_SERVICE_NAME.
const ServiceName = "boomtime"

// Enabled reports whether an OTLP endpoint is configured.
func Enabled() bool { return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" }

// Setup installs a global TracerProvider + W3C propagator and returns a
// shutdown func that flushes pending spans. Safe to call when disabled.
func Setup(ctx context.Context, logger *slog.Logger, version string) (func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }
	if !Enabled() {
		logger.Info("tracing: disabled (OTEL_EXPORTER_OTLP_ENDPOINT unset)")
		return noop, nil
	}

	// otlptracehttp reads OTEL_EXPORTER_OTLP_ENDPOINT/HEADERS/etc from the
	// environment, so the endpoint is configured entirely by deployment.
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return noop, err
	}

	name := os.Getenv("OTEL_SERVICE_NAME")
	if name == "" {
		name = ServiceName
	}

	// Build the resource with plain attribute keys rather than a pinned
	// semconv package — semconv majors churn and would break builds on every
	// OTel bump for zero benefit here.
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			resource.Default().SchemaURL(),
			attribute.String("service.name", name),
			attribute.String("service.version", version),
		),
	)
	if err != nil {
		return noop, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		// Homelab volume is tiny; sample everything so a single slow request
		// (e.g. the ~6.4s p95 on /heartbeats.bulk) is always captured.
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	logger.Info("tracing: enabled",
		"service", name,
		"endpoint", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))

	return tp.Shutdown, nil
}
