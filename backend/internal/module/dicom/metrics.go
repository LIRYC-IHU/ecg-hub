package dicom

import "github.com/prometheus/client_golang/prometheus"

var (
	dicomParseModality = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "module_dicom_parse_modality_total",
		Help: "Total DICOM files parsed by modality tag (0008,0060).",
	}, []string{"modality"})
)

// Collectors implements module.MetricsProvider so main.go registers these
// collectors when the dicom module is active.
func (m *Module) Collectors() []prometheus.Collector {
	return []prometheus.Collector{dicomParseModality}
}

// recordModality increments the modality counter after a successful Parse.
func recordModality(modality string) {
	if modality == "" {
		modality = "unknown"
	}
	dicomParseModality.WithLabelValues(modality).Inc()
}
