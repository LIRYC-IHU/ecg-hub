package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	ConnectorRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "connector_requests_total",
		Help: "Total outbound connector requests by connector name and status (sent, failed, exhausted).",
	}, []string{"connector", "status"})

	ConnectorRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "connector_request_duration_seconds",
		Help:    "Duration of outbound connector Forward calls.",
		Buckets: []float64{.1, .5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"connector", "op"})

	// DICOM-specific metrics — emitted by connector/dicom package.

	ConnectorDICOMCStoreTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "connector_dicom_cstore_total",
		Help: "Total DICOM C-STORE requests by connector and outcome.",
	}, []string{"connector", "status"})

	ConnectorDICOMCStoreDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "connector_dicom_cstore_duration_seconds",
		Help:    "Duration of DICOM C-STORE operations.",
		Buckets: []float64{.1, .5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"connector"})

	ConnectorDICOMCEchoTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "connector_dicom_cecho_total",
		Help: "Total DICOM C-ECHO (health check) requests by connector and outcome.",
	}, []string{"connector", "status"})

	ConnectorDICOMAssociationErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "connector_dicom_association_errors_total",
		Help: "Total DICOM association errors by connector and reason.",
	}, []string{"connector", "reason"})

	ConnectorDICOMBytesSentTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "connector_dicom_bytes_sent_total",
		Help: "Total bytes sent via DICOM C-STORE by connector.",
	}, []string{"connector"})
)

func init() {
	Registry.MustRegister(
		ConnectorRequestsTotal,
		ConnectorRequestDuration,
		ConnectorDICOMCStoreTotal,
		ConnectorDICOMCStoreDuration,
		ConnectorDICOMCEchoTotal,
		ConnectorDICOMAssociationErrorsTotal,
		ConnectorDICOMBytesSentTotal,
	)
}
