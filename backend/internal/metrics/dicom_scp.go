package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	DICOMSCPFilesReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "dicom_scp_files_received_total",
		Help: "Total DICOM files received via C-STORE SCP.",
	})

	DICOMSCPBytesReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "dicom_scp_bytes_received_total",
		Help: "Total bytes received via DICOM C-STORE SCP.",
	})

	DICOMSCPCEchoReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "dicom_scp_cecho_received_total",
		Help: "Total C-ECHO verification requests received by the SCP.",
	})

	DICOMSCPErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dicom_scp_errors_total",
		Help: "Total DICOM SCP errors by kind (reconstruct, queue_full).",
	}, []string{"kind"})

	ConnectorHealthStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "connector_health_status",
		Help: "Connector health: 1 = healthy, 0 = unhealthy.",
	}, []string{"connector", "protocol"})
)

func init() {
	Registry.MustRegister(
		DICOMSCPFilesReceived,
		DICOMSCPBytesReceived,
		DICOMSCPCEchoReceived,
		DICOMSCPErrors,
		ConnectorHealthStatus,
	)
}
