package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/hl7"
)

type fakeORUSender struct {
	gotECGID       string
	gotTriggeredBy string
	attempt        *models.HL7ORUAttempt
	err            error
}

func (f *fakeORUSender) SendForECG(_ context.Context, ecgID, triggeredBy string) (*models.HL7ORUAttempt, error) {
	f.gotECGID = ecgID
	f.gotTriggeredBy = triggeredBy
	return f.attempt, f.err
}

func newSendCtx(t *testing.T, ecgID, userID string) (echo.Context, *httptest.ResponseRecorder) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ecgs/"+ecgID+"/send-result", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(ecgID)
	c.Set(mw.CtxKeyUserID, userID)
	return c, rec
}

func TestSendECGResult_Disabled(t *testing.T) {
	f := &fakeORUSender{err: hl7.ErrORUDisabled}
	c, rec := newSendCtx(t, "ecg-1", "user-9")

	if err := SendECGResultHandler(f, nil)(c); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
	if f.gotTriggeredBy != "user-9" {
		t.Errorf("triggeredBy = %q, want user-9 (the caller)", f.gotTriggeredBy)
	}
	if f.gotECGID != "ecg-1" {
		t.Errorf("ecgID = %q, want ecg-1", f.gotECGID)
	}
}

func TestSendECGResult_NoDestination(t *testing.T) {
	f := &fakeORUSender{err: hl7.ErrORUNoDestination}
	c, rec := newSendCtx(t, "ecg-1", "user-9")
	_ = SendECGResultHandler(f, nil)(c)
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestSendECGResult_NoPatient(t *testing.T) {
	f := &fakeORUSender{err: hl7.ErrORUNoPatient}
	c, rec := newSendCtx(t, "ecg-1", "user-9")
	_ = SendECGResultHandler(f, nil)(c)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}
