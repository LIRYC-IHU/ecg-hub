package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/auth"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/dbtest"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/tenancy"
)

// tokenProvider stands in for the JWT validation: a known token is a known
// user. Everything after it is the real chain — identity and tenant read from
// the database, tenant written to the session, Row Level Security.
type tokenProvider map[string]string

func (p tokenProvider) ValidateToken(_ context.Context, token string) (*auth.Claims, error) {
	sub, ok := p[token]
	if !ok {
		return nil, errors.New("unknown token")
	}
	return &auth.Claims{Sub: sub, Role: "writer", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

type practice struct {
	tenantID, ecgID, quarantineID, tagID string
	file, fileContent                    string
}

// seedPractice creates a tenant with a user and one of everything. Every
// practice numbers its patient "P1" and stores the same file hash: neither
// may collide with, or reveal, another's.
func seedPractice(t *testing.T, gdb *gorm.DB, slug, username, dir string) practice {
	t.Helper()
	bg := context.Background()
	// The same bytes everywhere: the download checks the stored hash, and a
	// file one practice holds must not count as a duplicate in another.
	p := practice{fileContent: "the same ECG file"}
	if slug == "default" {
		p.tenantID = models.DefaultTenantID
	} else if err := gdb.Raw(`INSERT INTO tenants (slug, name) VALUES (?, ?) RETURNING id`, slug, slug).Scan(&p.tenantID).Error; err != nil {
		t.Fatalf("create tenant %s: %v", slug, err)
	}
	users := repository.NewUserRepo(gdb)
	if _, err := users.UpsertLogin(bg, "", username, "oidc", []string{"writer"}); err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	if slug != "default" {
		if err := users.BindTenant(bg, username, slug); err != nil {
			t.Fatalf("bind %s to %s: %v", username, slug, err)
		}
	}

	ctx := tenancy.With(bg, p.tenantID)
	if err := repository.NewPatientRepository(gdb).UpsertWithDemographics(ctx, "P1", "Jane", slug, "F"); err != nil {
		t.Fatalf("patient for %s: %v", slug, err)
	}
	p.file = filepath.Join(dir, slug+".xml")
	if err := os.WriteFile(p.file, []byte(p.fileContent), 0o600); err != nil {
		t.Fatal(err)
	}
	ecg := &models.ECG{PatientID: "P1", Vendor: "test", FilePath: p.file, OriginalFilename: slug + ".xml", ContentHash: sha256Hex(p.fileContent)}
	if err := repository.NewECGRepository(gdb).Insert(ctx, ecg); err != nil {
		t.Fatalf("ECG for %s: %v", slug, err)
	}
	q := &models.QuarantineEntry{Filename: slug + ".bin", FilePath: filepath.Join(dir, slug+".bin"), ErrorReason: "no_adapter"}
	if err := repository.NewQuarantineRepository(gdb).Insert(ctx, q); err != nil {
		t.Fatalf("quarantine for %s: %v", slug, err)
	}
	tag, err := repository.NewTagRepository(gdb).CreateTag(ctx, "tag-"+slug, "#000000", username)
	if err != nil {
		t.Fatalf("tag for %s: %v", slug, err)
	}
	p.ecgID, p.quarantineID, p.tagID = ecg.ID, q.ID, tag.ID
	return p
}

// Through the API, as an authenticated user of one practice: lists hold only
// that practice's rows, and another practice's ECG, quarantine entry or tag
// cannot be read, changed, exported, downloaded or deleted by its id. The
// platform tenant is no exception in either direction.
func TestAPI_TenantIsolation(t *testing.T) {
	gdb := dbtest.OpenMultiTenant(t)
	dir := t.TempDir()
	a := seedPractice(t, gdb, "cab-a", "dr-a", dir)
	b := seedPractice(t, gdb, "cab-b", "dr-b", dir)
	platform := seedPractice(t, gdb, "default", "ops", dir)
	foreign := []practice{a, platform} // everything dr-b must not reach

	provider := tokenProvider{"tok-a": "dr-a", "tok-b": "dr-b", "tok-ops": "ops"}
	users := repository.NewUserRepo(gdb)
	system := tenancy.System(context.Background())

	// The context a Connect handler receives after the auth interceptor.
	connectCtx := func(token string) context.Context {
		t.Helper()
		var got context.Context
		req := connect.NewRequest(&apiv1.ListTagsRequest{})
		req.Header().Set("Authorization", "Bearer "+token)
		next := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
			got = ctx
			return nil, nil
		}
		if _, err := mw.ConnectRequireAuth(provider, users, nil)(next)(context.Background(), req); err != nil {
			t.Fatalf("authenticate %s: %v", token, err)
		}
		return got
	}
	ctxB := connectCtx("tok-b")

	ecgSvc := &ECGServiceHandler{DB: gdb}
	patientSvc := &PatientServiceHandler{DB: gdb}
	tagSvc := &TagServiceHandler{Repo: repository.NewTagRepository(gdb)}
	adminSvc := &AdminServiceHandler{DB: gdb}
	exportSvc := &ExportServiceHandler{ECGRepo: repository.NewECGRepository(gdb)}

	// ── Lists ────────────────────────────────────────────────────────────────
	for token, own := range map[string]practice{"tok-a": a, "tok-b": b, "tok-ops": platform} {
		all, err := ecgSvc.ListAll(connectCtx(token), &apiv1.ListAllRequest{})
		if err != nil {
			t.Fatalf("%s ListAll: %v", token, err)
		}
		if len(all.Data) != 1 || all.Data[0].Ecg.Id != own.ecgID {
			t.Errorf("%s lists %d ECGs, want only its own", token, len(all.Data))
		}
	}
	if res, err := patientSvc.Search(ctxB, &apiv1.SearchRequest{}); err != nil || res.Total != 1 {
		t.Errorf("dr-b patient search: total=%v err=%v, want 1", res.GetTotal(), err)
	}
	if res, err := patientSvc.ListECGs(ctxB, &apiv1.ListECGsRequest{PatientId: "P1"}); err != nil || len(res.Data) != 1 || res.Data[0].Id != b.ecgID {
		t.Errorf("dr-b ECGs of P1: %v err=%v, want only its own", res.GetData(), err)
	}
	if res, err := tagSvc.ListTags(ctxB, &apiv1.ListTagsRequest{}); err != nil || len(res.Data) != 1 || res.Data[0].Id != b.tagID {
		t.Errorf("dr-b tags: %v err=%v, want only its own", res.GetData(), err)
	}
	if res, err := adminSvc.ListQuarantine(ctxB, &apiv1.ListQuarantineRequest{}); err != nil || res.Total != 1 || res.Data[0].Id != b.quarantineID {
		t.Errorf("dr-b quarantine: total=%v err=%v, want only its own", res.GetTotal(), err)
	}

	// ── By id, on another tenant's rows ──────────────────────────────────────
	for _, f := range foreign {
		if _, err := ecgSvc.GetMetadata(ctxB, &apiv1.GetMetadataRequest{Id: f.ecgID}); connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("dr-b reads a foreign ECG's metadata: %v", err)
		}
		_, _ = ecgSvc.MarkViewed(ctxB, &apiv1.MarkViewedRequest{Id: f.ecgID})
		var viewed int64
		gdb.WithContext(system).Raw(`SELECT count(*) FROM ecgs WHERE id = ? AND viewed_at IS NOT NULL`, f.ecgID).Scan(&viewed)
		if viewed != 0 {
			t.Error("dr-b marked a foreign ECG as viewed")
		}
		if _, err := tagSvc.TagEcg(ctxB, &apiv1.TagEcgRequest{EcgId: f.ecgID, TagId: b.tagID}); err == nil {
			t.Error("dr-b tagged a foreign ECG")
		}
		if _, err := tagSvc.TagEcg(ctxB, &apiv1.TagEcgRequest{EcgId: b.ecgID, TagId: f.tagID}); err == nil {
			t.Error("dr-b applied a foreign tag")
		}
		if _, err := adminSvc.DeleteQuarantine(ctxB, &apiv1.DeleteQuarantineRequest{Id: f.quarantineID}); connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("dr-b deleted a foreign quarantine entry: %v", err)
		}
		if _, err := exportSvc.Create(ctxB, &apiv1.CreateExportRequest{EcgIds: []string{f.ecgID}, Formats: []string{"original"}}); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("dr-b exported a foreign ECG: %v", err)
		}
	}

	// ── REST routes, through the Echo auth middleware ────────────────────────
	e := echo.New()
	api := e.Group("", mw.AuthMiddleware(provider, users, nil))
	api.GET("/ecgs", ListAllECGsHandler(gdb))
	api.GET("/ecgs/:id/download", DownloadECGHandler(gdb, nil))
	api.DELETE("/ecgs/:id", DeleteECGHandler(gdb))
	call := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	if rec := call(http.MethodGet, "/ecgs", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401", rec.Code)
	}
	if rec := call(http.MethodGet, "/ecgs", "tok-b"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), b.ecgID) ||
		strings.Contains(rec.Body.String(), a.ecgID) || strings.Contains(rec.Body.String(), platform.ecgID) {
		t.Errorf("dr-b REST list: status %d, must hold its ECG and no other", rec.Code)
	}
	if rec := call(http.MethodGet, "/ecgs/"+b.ecgID+"/download", "tok-b"); rec.Code != http.StatusOK || rec.Body.String() != b.fileContent {
		t.Errorf("dr-b downloads its own ECG: status %d body %q", rec.Code, rec.Body.String())
	}
	for _, f := range foreign {
		if rec := call(http.MethodGet, "/ecgs/"+f.ecgID+"/download", "tok-b"); rec.Code != http.StatusNotFound {
			t.Errorf("dr-b downloads a foreign ECG: status %d", rec.Code)
		}
		if rec := call(http.MethodDelete, "/ecgs/"+f.ecgID, "tok-b"); rec.Code != http.StatusNotFound {
			t.Errorf("dr-b deletes a foreign ECG: status %d", rec.Code)
		}
	}

	// Nothing of the other tenants was touched.
	var left int64
	gdb.WithContext(system).Raw(`SELECT (SELECT count(*) FROM ecgs) + (SELECT count(*) FROM quarantine_entries)`).Scan(&left)
	if left != 6 {
		t.Errorf("%d ECG and quarantine rows remain, want all 6", left)
	}
	for _, f := range foreign {
		if _, err := os.Stat(f.file); err != nil {
			t.Errorf("a foreign ECG file is gone: %v", err)
		}
	}
}
