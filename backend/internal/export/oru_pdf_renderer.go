package export

import (
	"context"
	"github.com/LIRYC-IHU/ecg-hub/internal/storage"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// ORUPDFRenderer adapts the export Converter to the PDF-rendering interface consumed
// by the HL7 ORU service (hl7.PDFRenderer), keeping the hl7 package decoupled from
// the export bridge. It renders the ECG's printable PDF report, injecting the local
// (HL7-enriched) patient demographics so the report and the ORU PID stay consistent.
type ORUPDFRenderer struct {
	conv Converter
}

// NewORUPDFRenderer wraps a Converter for use as an hl7.PDFRenderer.
func NewORUPDFRenderer(conv Converter) *ORUPDFRenderer {
	return &ORUPDFRenderer{conv: conv}
}

// RenderPDF produces the PDF report bytes for the given ECG. When a patient record is
// available its demographics are injected into the rendered report.
func (r *ORUPDFRenderer) RenderPDF(ctx context.Context, ecg *models.ECG, patient *models.Patient) ([]byte, error) {
	opts := ConvertOptions{InjectPatient: patient != nil}
	localPath, cleanup, err := storage.Materialize(ctx, ecg.FilePath)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return r.conv.Convert(ctx, localPath, ecg.Vendor, "pdf", patient, opts)
}
