package handlers

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
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

// sendResult drives HL7ServiceHandler.SendResult with the caller identity in the
// context (as the auth interceptor would set it). db is nil: pre-send guards
// return before any audit write, so it is never touched.
func sendResult(f *fakeORUSender, ecgID, userID string) error {
	h := &HL7ServiceHandler{ORUService: f}
	ctx := mw.ContextWithIdentity(context.Background(), userID, userID, "")
	_, err := h.SendResult(ctx, &apiv1.SendResultRequest{EcgId: ecgID})
	return err
}

func TestSendResult_Disabled(t *testing.T) {
	f := &fakeORUSender{err: hl7.ErrORUDisabled}
	err := sendResult(f, "ecg-1", "user-9")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
	if f.gotTriggeredBy != "user-9" {
		t.Errorf("triggeredBy = %q, want user-9 (the caller)", f.gotTriggeredBy)
	}
	if f.gotECGID != "ecg-1" {
		t.Errorf("ecgID = %q, want ecg-1", f.gotECGID)
	}
}

func TestSendResult_NoDestination(t *testing.T) {
	f := &fakeORUSender{err: hl7.ErrORUNoDestination}
	err := sendResult(f, "ecg-1", "user-9")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}

func TestSendResult_NoPatient(t *testing.T) {
	f := &fakeORUSender{err: hl7.ErrORUNoPatient}
	err := sendResult(f, "ecg-1", "user-9")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}

func TestSendResult_ServiceUnavailable(t *testing.T) {
	// No ORU service wired → FailedPrecondition, without touching the sender.
	h := &HL7ServiceHandler{ORUService: nil}
	_, err := h.SendResult(context.Background(), &apiv1.SendResultRequest{EcgId: "ecg-1"})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}
