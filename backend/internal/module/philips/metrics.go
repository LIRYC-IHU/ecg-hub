package philips

import "github.com/prometheus/client_golang/prometheus"

var (
	philipsDocTypeTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "module_philips_doc_type_total",
		Help: "Total Philips ECG files parsed by document type and version.",
	}, []string{"type", "version"})

	philipsMissingPatientID = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "module_philips_missing_patient_id_total",
		Help: "Total Philips files rejected due to missing patient ID during Validate.",
	})
)

// Collectors implements module.MetricsProvider so main.go registers these
// collectors when the philips module is active.
func (m *Module) Collectors() []prometheus.Collector {
	return []prometheus.Collector{philipsDocTypeTotal, philipsMissingPatientID}
}

func recordDocType(docType, docVersion string) {
	philipsDocTypeTotal.WithLabelValues(docType, docVersion).Inc()
}

func recordMissingPatientID() {
	philipsMissingPatientID.Inc()
}
