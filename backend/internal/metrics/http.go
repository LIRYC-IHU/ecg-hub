// Story 1.2 — HTTP RED middleware (requests_total, request_duration_seconds, in_flight).
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	httpRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests.",
	}, []string{"method", "route", "status"})

	httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"method", "route"})

	httpInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "http_requests_in_flight",
		Help: "Current number of HTTP requests being served.",
	})
)

func init() {
	Registry.MustRegister(httpRequestsTotal, httpRequestDuration, httpInFlight)
}

// Middleware returns an Echo middleware that instruments every request.
// The route label uses the Echo path pattern (e.g. /api/v1/ecgs/:id) to avoid
// unbounded cardinality from path parameter values.
func Middleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			httpInFlight.Inc()
			start := time.Now()

			err := next(c)

			httpInFlight.Dec()

			route := c.Path()
			if route == "" {
				route = c.Request().URL.Path
			}
			status := strconv.Itoa(c.Response().Status)
			if err != nil {
				if he, ok := err.(*echo.HTTPError); ok {
					status = strconv.Itoa(he.Code)
				} else {
					status = strconv.Itoa(http.StatusInternalServerError)
				}
			}

			httpRequestsTotal.WithLabelValues(c.Request().Method, route, status).Inc()
			httpRequestDuration.WithLabelValues(c.Request().Method, route).Observe(time.Since(start).Seconds())

			return err
		}
	}
}
