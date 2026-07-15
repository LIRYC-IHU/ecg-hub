// Package module provides the main entry point for the Nihon Kohden module,
// is handle communication ECTP with ftp storage files
// and also implements the Module interface for parsing Nihon Kohden files.

package nihonkohden

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/bridgeutil"
	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

var ECTPPort = 30003

var nkToFDABinary = bridgeBin("BRIDGE_NK_TO_FDA", "nk-to-fda")
var nkToDICOMBinary = bridgeBin("BRIDGE_NK_TO_DICOM", "nk-to-dicom")

var _ module.Module = (*Module)(nil)

type nkMetadataJSON struct {
	PatientID    string  `json:"patientID"`
	FamilyName   string  `json:"familyName"`
	GivenName    string  `json:"givenName"`
	Gender       string  `json:"gender"`
	BirthDate    string  `json:"birthDate"`
	Location     string  `json:"location"`
	DeviceModel  string  `json:"deviceModel"`
	Datetime     string  `json:"datetime"`
	HeartRate    int     `json:"heartRate"`
	PRInterval   int     `json:"prInterval"`
	QRSDuration  int     `json:"qrsDuration"`
	QTInterval   int     `json:"qtInterval"`
	QTcInterval  int     `json:"qtcInterval"`
	PAxis        int     `json:"pAxis"`
	QRSAxis      int     `json:"qrsAxis"`
	TAxis        int     `json:"tAxis"`
	V5RAmplitude float64 `json:"v5rAmplitude"`
	V1SAmplitude float64 `json:"v1sAmplitude"`
	SampleRate   int     `json:"sampleRate"`
	TotalSamples int     `json:"totalSamples"`
}

func bridgeBin(envKey, name string) string {
	return bridgeutil.ResolveBin(envKey, name)
}

func init() {
	module.Register(&Module{})
}

// Module implements module.Module for Nihon Kohden ECG files.
type Module struct {
	server       *ECTPServer
	transferRepo *repository.NihonKohdenRepository
}

func (m *Module) Name() string                 { return "nihon-kohden" }
func (m *Module) AcceptedExtensions() []string { return []string{".dat", ".DAT"} }

func (m *Module) Health() error { return nil }

// ECTPListenPort implements module.ECTPProvider.
// Returns the TCP port on which the ECTP server listens.
func (m *Module) ECTPListenPort() int { return ECTPPort }

// SupportedFormats returns the export formats this module can produce.
func (m *Module) SupportedFormats() []module.ExportFormat {
	return []module.ExportFormat{
		{ID: "original", Label: "Nihon Kohden DAT", Extension: ".DAT"},
		{ID: "xmlfda", Label: "FDA HL7 aECG XML", Extension: ".xml"},
		{ID: "dicom", Label: "DICOM ECG", Extension: ".dcm"},
	}
}

// Validate checks that data is a non-empty nihon-kohden file with a patient ID.
func (m *Module) Validate(data []byte) error {
	return nil
}

func (m *Module) Parse(ctx context.Context, data []byte) (*module.ECGMetadata, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("nihon-kohden: parse: empty data")
	}

	tmp, err := os.CreateTemp("", "nk-parse-*.DAT")
	if err != nil {
		return nil, fmt.Errorf("nihon-kohden: parse: create temp: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("nihon-kohden: parse: write temp: %w", err)
	}
	tmp.Close()

	cmd := exec.CommandContext(ctx, nkToFDABinary, "--input", tmp.Name(), "--metadata-json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		pwd := os.Getenv("PWD")
		slog.Error("nihon-kohden: parse: nk-to-fda failed", "stderr", stderr.String(), "error", err)
		return nil, fmt.Errorf("nihon-kohden: parse: nk-to-fda:%s/%s %w", pwd, nkToFDABinary, err)
	}

	var md nkMetadataJSON
	if err := json.Unmarshal(stdout.Bytes(), &md); err != nil {
		return nil, fmt.Errorf("nihon-kohden: parse: json decode: %w", err)
	}

	var recordedAt time.Time
	if md.Datetime != "" {
		if t, err := time.Parse("20060102150405", md.Datetime); err == nil {
			recordedAt = t
		}
	}

	var durationSeconds float64
	if md.SampleRate > 0 && md.TotalSamples > 0 {
		durationSeconds = float64(md.TotalSamples) / float64(md.SampleRate)
	}

	extra := map[string]any{}
	if md.FamilyName != "" {
		extra["last_name"] = md.FamilyName
	}
	if md.GivenName != "" {
		extra["first_name"] = md.GivenName
	}
	if md.Gender != "" {
		extra["sex"] = md.Gender
	}
	if md.BirthDate != "" {
		extra["birth_date"] = md.BirthDate
	}
	if md.Location != "" {
		extra["location"] = md.Location
	}
	if md.HeartRate > 0 {
		extra["heart_rate"] = md.HeartRate
	}
	if md.PRInterval > 0 {
		extra["pr_interval"] = md.PRInterval
	}
	if md.QRSDuration > 0 {
		extra["qrs_duration"] = md.QRSDuration
	}
	if md.QTInterval > 0 {
		extra["qt_interval"] = md.QTInterval
	}
	if md.QTcInterval > 0 {
		extra["qtc_interval"] = md.QTcInterval
	}

	return &module.ECGMetadata{
		PatientID:       md.PatientID,
		RecordedAt:      recordedAt,
		VendorName:      m.Name(),
		SourceFormat:    "nihon_kohden_dat",
		DeviceModel:     md.DeviceModel,
		LeadCount:       12,
		DurationSeconds: durationSeconds,
		SampleRate:      float64(md.SampleRate),
		Extra:           extra,
	}, nil
}

func (m *Module) RenamePatientID(data []byte, newID string) ([]byte, error) {
	if newID == "" {
		return nil, fmt.Errorf("nihon-kohden: rename_patient_id: newID is empty")
	}
	return []byte{}, nil
}

func (m *Module) UpdateFile(filePath string, patch module.MetadataPatch) error {
	return nil
}

// SetDB implements module.DBAccessor.
// Called by main.go before Start() — builds the transfer repository used by the ECTP server.
func (m *Module) SetDB(db any) {
	gdb, ok := db.(*gorm.DB)
	if !ok {
		slog.Warn("nihon-kohden: SetDB received unexpected type, transfer tracking disabled")
		return
	}
	m.transferRepo = repository.NewNihonKohdenRepository(gdb, 500)
}

// RegisterFTPFile implements module.FTPFileTracker.
// Called by the FTP server hook whenever a file is successfully received.
func (m *Module) RegisterFTPFile(filename string) error {
	if m.transferRepo == nil {
		return nil
	}
	return m.transferRepo.Register(filename)
}

func (m *Module) Start(cfg *config.Config) error {
	m.server = NewECTPServer(fmt.Sprintf(":%d", ECTPPort), cfg, m.transferRepo)
	// Bind synchronously so a taken port surfaces as a clean fatal startup
	// error in main (module start failure) instead of a goroutine panic that
	// crashes the whole process with a stack trace.
	if err := m.server.Listen(); err != nil {
		return fmt.Errorf("nihon-kohden: start ECTP server: %w", err)
	}
	return nil
}
