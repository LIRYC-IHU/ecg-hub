package db

import (
	"context"
	"errors"
	"testing"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/tenancy"
)

// Through the repositories, as the handlers and the ingestion path use them:
// a row written for one tenant gets its tenant without being told, and is
// invisible to the other — deduplication included.
func TestTenancy_RepositoriesAreScoped(t *testing.T) {
	app, _ := openAsAppRole(t)
	if err := RunMigrations(app); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	if err := ApplyTenancy(app, true); err != nil {
		t.Fatalf("ApplyTenancy: %v", err)
	}
	tenancy.Configure(true, "organization")
	t.Cleanup(func() { tenancy.Configure(false, "") })

	var b string
	if err := app.Raw(`INSERT INTO tenants (slug, name) VALUES ('cabinet-b', 'B') RETURNING id`).Scan(&b).Error; err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	bg := context.Background()
	ctxA, ctxB := tenancy.With(bg, models.DefaultTenantID), tenancy.With(bg, b)

	patients := repository.NewPatientRepository(app)
	ecgs := repository.NewECGRepository(app)
	tags := repository.NewTagRepository(app)
	quarantine := repository.NewQuarantineRepository(app)

	// Same patient identifier and same file in both tenants.
	for _, ctx := range []context.Context{ctxB, ctxA} {
		if err := patients.UpsertWithDemographics(ctx, "P1", "Jane", "Doe", "F"); err != nil {
			t.Fatalf("upsert patient: %v", err)
		}
	}
	ecgB := &models.ECG{PatientID: "P1", Vendor: "test", FilePath: "/b", OriginalFilename: "b.xml", ContentHash: "same"}
	if err := ecgs.Insert(ctxB, ecgB); err != nil {
		t.Fatalf("insert ECG for B: %v", err)
	}
	if ecgB.TenantID != b {
		t.Errorf("ECG written for B carries tenant %q, want %q", ecgB.TenantID, b)
	}
	if exists, _ := ecgs.ExistsByContentHash(ctxA, "same"); exists {
		t.Error("tenant A sees B's file as a duplicate")
	}
	if err := ecgs.Insert(ctxA, &models.ECG{PatientID: "P1", Vendor: "test", FilePath: "/a", OriginalFilename: "a.xml", ContentHash: "same"}); err != nil {
		t.Errorf("same file in tenant A refused: %v", err)
	}
	if _, err := ecgs.FindByID(ctxA, ecgB.ID); !errors.Is(err, repository.ErrECGNotFound) {
		t.Errorf("tenant A reads B's ECG: err=%v", err)
	}
	if got, err := ecgs.FindByID(ctxB, ecgB.ID); err != nil || got.ID != ecgB.ID {
		t.Errorf("tenant B cannot read its own ECG: %v", err)
	}

	if _, err := tags.CreateTag(ctxB, "urgent", "#ff0000", "dr-b"); err != nil {
		t.Fatalf("create tag: %v", err)
	}
	if list, _ := tags.ListTags(ctxA); len(list) != 0 {
		t.Errorf("tenant A lists %d of B's tags", len(list))
	}

	if err := quarantine.Insert(ctxB, &models.QuarantineEntry{Filename: "x.bin", FilePath: "/q", ErrorReason: "no_adapter"}); err != nil {
		t.Fatalf("insert quarantine entry: %v", err)
	}
	if _, total, _ := quarantine.List(ctxA, 1, 50, ""); total != 0 {
		t.Errorf("tenant A lists %d of B's quarantine entries", total)
	}
	if _, total, _ := quarantine.List(ctxB, 1, 50, ""); total != 1 {
		t.Errorf("tenant B lists %d quarantine entries, want 1", total)
	}
}
