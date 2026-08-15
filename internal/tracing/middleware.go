package tracing

import (
	"fmt"

	"github.com/labstack/echo/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/TheBranchDriftCatalyst/boomtime"

// Middleware produces server spans for every request.
//
// Hand-rolled because go.opentelemetry.io/contrib/.../otelecho still targets
// echo/v4 while boomtime runs echo/v5 (verified 2026-08-15: otelecho v0.70.0
// requires github.com/labstack/echo/v4).
func Middleware() echo.MiddlewareFunc {
	tracer := otel.Tracer(tracerName)
	prop := otel.GetTextMapPropagator()

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if !Enabled() {
				return next(c)
			}
			req := c.Request()

			// Continue an upstream trace if the caller sent W3C headers.
			ctx := prop.Extract(req.Context(), propagation.HeaderCarrier(req.Header))

			// Span name uses the ROUTE TEMPLATE (c.Path(), e.g. /p/:slug), never
			// the raw URL — raw paths would explode span cardinality, same
			// reasoning as the metrics middleware.
			route := c.Path()
			if route == "" {
				route = "unmatched"
			}

			ctx, span := tracer.Start(ctx,
				fmt.Sprintf("%s %s", req.Method, route),
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("http.request.method", req.Method),
					attribute.String("http.route", route),
					attribute.String("url.path", req.URL.Path),
					attribute.String("server.address", req.Host),
					attribute.String("user_agent.original", req.UserAgent()),
				),
			)
			defer span.End()

			// Hand the traced context down so handlers, DB calls and the slog
			// trace_id injection all see the span.
			c.SetRequest(req.WithContext(ctx))

			err := next(c)

			// echo/v5 exposes the response via http.ResponseWriter; the
			// concrete *echo.Response carries the status (same assertion the
			// metrics middleware uses).
			status := 0
			if resp, ok := c.Response().(*echo.Response); ok {
				status = resp.Status
			}
			if err != nil {
				// Echo may not have written the status yet when a handler
				// returns an error.
				if he, ok := err.(*echo.HTTPError); ok {
					status = he.Code
				}
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			} else if status >= 500 {
				span.SetStatus(codes.Error, fmt.Sprintf("status %d", status))
			}
			span.SetAttributes(attribute.Int("http.response.status_code", status))

			return err
		}
	}
}
