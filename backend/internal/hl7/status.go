package hl7

// HL7 enrichment status values — stored in ecgs.hl7_status column.
const (
	StatusPending   = "pending"
	StatusSuccess   = "success"
	StatusExhausted = "hl7_exhausted" // retries exhausted (network/timeout/no PID)
	StatusRejected  = "hl7_rejected"  // HIS rejected the query (MSA AE/AR) after retries
)
