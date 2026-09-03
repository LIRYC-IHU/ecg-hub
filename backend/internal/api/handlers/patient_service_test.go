package handlers

import (
	"context"
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

// MarkECGsViewed took whatever identifier the caller had and handed it straight
// to a query on ecgs.patient_id, which stores the device string. The UI sends
// the UUID, so the query matched nothing and every call reported zero marked
// while the unviewed badge stayed put.
func TestMarkECGsViewed_AcceptsEitherPatientIdentifier(t *testing.T) {
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

	h := &PatientServiceHandler{DB: db}

	for _, tc := range []struct {
		name string
		// which identifier the caller sends
		useUUID bool
	}{
		{"device string", false},
		{"UUID", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deviceID := "TESTPID-" + tc.name
			patient := models.Patient{PatientID: deviceID, LastName: "Test"}
			if err := db.Create(&patient).Error; err != nil {
				t.Fatalf("create patient: %v", err)
			}
			ecg := models.ECG{
				PatientID:        deviceID,
				Vendor:           "test",
				FilePath:         "/dev/null",
				OriginalFilename: "t.xml",
				ContentHash:      "hash-" + deviceID,
			}
			if err := db.Create(&ecg).Error; err != nil {
				t.Fatalf("create ecg: %v", err)
			}
			t.Cleanup(func() {
				db.Unscoped().Delete(&ecg)
				db.Unscoped().Delete(&patient)
			})

			id := deviceID
			if tc.useUUID {
				id = patient.ID
			}
			res, err := h.MarkECGsViewed(context.Background(), &apiv1.MarkECGsViewedRequest{PatientId: id})
			if err != nil {
				t.Fatalf("MarkECGsViewed: %v", err)
			}
			if res.Marked != 1 {
				t.Errorf("marked = %d, want 1 (identifier %q matched no ECGs)", res.Marked, id)
			}
		})
	}
}
