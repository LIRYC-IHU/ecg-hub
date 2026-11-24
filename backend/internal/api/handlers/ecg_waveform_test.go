package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// A shared link names both a patient and an ECG, and the viewer prints the
// patient's identity above the trace. The waveform must not be served under a
// patient the ECG is not filed under, whatever the URL pairs it with.
func TestECGWaveform_RefusesAnECGFiledUnderAnotherPatient(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run integration tests")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open DB: %v", err)
	}

	owner := models.Patient{PatientID: "TESTPID-WAVEFORM-OWNER", LastName: "Owner"}
	other := models.Patient{PatientID: "TESTPID-WAVEFORM-OTHER", LastName: "Other"}
	for _, p := range []*models.Patient{&owner, &other} {
		if err := db.Create(p).Error; err != nil {
			t.Fatalf("create patient: %v", err)
		}
	}

	// A real file whose hash does not match: a request that gets past the
	// pairing check stops at the integrity check, which tells the two apart
	// without needing a parsable waveform.
	file := filepath.Join(t.TempDir(), "ecg.dcm")
	if err := os.WriteFile(file, []byte("not a waveform"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	ecg := models.ECG{
		PatientID:        owner.PatientID,
		Vendor:           "dicom",
		FilePath:         file,
		OriginalFilename: "ecg.dcm",
		ContentHash:      "hash-TESTPID-WAVEFORM",
	}
	if err := db.Create(&ecg).Error; err != nil {
		t.Fatalf("create ecg: %v", err)
	}
	t.Cleanup(func() {
		db.Unscoped().Delete(&ecg)
		db.Unscoped().Delete(&owner)
		db.Unscoped().Delete(&other)
	})

	for _, tc := range []struct {
		name     string
		query    string
		wantCode string
	}{
		{"another patient", "?patient=" + other.PatientID, "ECG_PATIENT_MISMATCH"},
		{"its own patient", "?patient=" + owner.PatientID, "INTEGRITY_FAILURE"},
		{"no patient named", "", "INTEGRITY_FAILURE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/ecgs/"+ecg.ID+"/waveform"+tc.query, nil)
			c := echo.New().NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(ecg.ID)

			if err := ECGWaveformHandler(db, "", nil)(c); err != nil {
				t.Fatalf("handler: %v", err)
			}
			var body struct {
				Code string `json:"code"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if body.Code != tc.wantCode {
				t.Errorf("code = %q (HTTP %d), want %q", body.Code, rec.Code, tc.wantCode)
			}
		})
	}
}
