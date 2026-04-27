package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	ModuleActive = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "module_active",
		Help: "1 if the module is active, 0 otherwise.",
	}, []string{"vendor"})

	ModuleHealth = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "module_health",
		Help: "1 if the module is healthy (Health() returns nil), 0 otherwise.",
	}, []string{"vendor"})

	ModuleParseDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "module_parse_duration_seconds",
		Help:    "Duration of module SafeParse calls.",
		Buckets: prometheus.DefBuckets,
	}, []string{"vendor"})

	ModuleParseErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "module_parse_errors_total",
		Help: "Total module parse errors by kind (parse, panic, validate).",
	}, []string{"vendor", "kind"})

	ModuleFilesAccepted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "module_files_accepted_total",
		Help: "Total files successfully parsed and accepted by a module.",
	}, []string{"vendor", "extension"})

	ModuleConversionDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "module_conversion_duration_seconds",
		Help:    "Duration of ECGBridge format conversion calls.",
		Buckets: []float64{.01, .05, .1, .5, 1, 2.5, 5, 10, 30},
	}, []string{"vendor", "format"})

	ModuleConversionErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "module_conversion_errors_total",
		Help: "Total ECGBridge conversion errors by vendor and format.",
	}, []string{"vendor", "format"})
)

func init() {
	Registry.MustRegister(
		ModuleActive,
		ModuleHealth,
		ModuleParseDuration,
		ModuleParseErrors,
		ModuleFilesAccepted,
		ModuleConversionDuration,
		ModuleConversionErrors,
	)
}
