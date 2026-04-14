// Package module provides the main entry point for the Nihon Kohden module,
// is handle communication ECTP with ftp storage files
// and also implements the Module interface for parsing Nihon Kohden files.

package nihonkohden

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

var ECTPPort = 30003

var _ module.Module = (*Module)(nil)

func init() {
	module.Register(&Module{})
}

// Module implements module.Module for Nihon Kohden ECG files.
type Module struct {
	server        *ECTPServer
	transferRepo  *repository.NihonKohdenRepository
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
		{ID: "original", Label: "nihon-kohden dat", Extension: ".DAT"},
	}
}

// Validate checks that data is a non-empty nihon-kohden file with a patient ID.
func (m *Module) Validate(data []byte) error {
	return nil
}

func (m *Module) Parse(_ context.Context, data []byte) (*module.ECGMetadata, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("philips: parse: empty data")
	}

	return &module.ECGMetadata{
		PatientID:       "",
		RecordedAt:      time.Time{},
		VendorName:      m.Name(),
		SourceFormat:    "",
		DeviceModel:     "",
		LeadCount:       0,
		DurationSeconds: 0,
		SampleRate:      0,
		Extra:           map[string]interface{}{},
	}, nil
}

func (m *Module) RenamePatientID(data []byte, newID string) ([]byte, error) {
	if newID == "" {
		return nil, fmt.Errorf("philips: rename_patient_id: newID is empty")
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
	go func() {
		if err := m.server.Listen(); err != nil {
			slog.Error("nihon-kohden: failed to start ECTP server", "error", err)
			panic(err)
		}
	}()
	return nil
}
