package models

import "time"

// Outcome values for an inbound ADT message.
const (
	InboundApplied = "applied" // a patient record changed
	InboundIgnored = "ignored" // valid, but nothing here to change
	InboundRefused = "refused" // turned away before it could be read
	InboundError   = "error"   // understood, could not be applied
)

// HL7InboundMessage records one ADT message received on the inbound listener
// (IHE RAD-12 Patient Update) and what became of it.
//
// It deliberately does NOT store the message body. An ADT carries the patient's
// name, date of birth and address, and this table exists to answer operational
// questions — did the feed arrive, was it accepted, why did nothing change — not
// to become a second copy of the demographics with its own retention problem.
// hl7_attempts takes the same line on the query side.
//
// Segments is the list of segment names the message carried, which is what tells
// an operator whether the feed sends what the mappings need: a PID without a
// PV1, an A40 without its MRG. Names, not values.
type HL7InboundMessage struct {
	ID         string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	ReceivedAt time.Time `gorm:"autoCreateTime;index:idx_hl7_inbound_received,sort:desc" json:"received_at"`

	TriggerEvent    string `gorm:"type:text;index" json:"trigger_event"` // A08, A40, ...
	MessageType     string `gorm:"type:text" json:"message_type"`        // normally ADT
	SendingFacility string `gorm:"type:text;index" json:"sending_facility"`
	ControlID       string `gorm:"type:text;index" json:"control_id"`
	// RemoteAddr is the peer as seen after any address translation in between,
	// which is not necessarily the sending system — see the listener's sender
	// allowlist for what that costs.
	RemoteAddr string `gorm:"type:text" json:"remote_addr"`
	Segments   string `gorm:"type:text" json:"segments"`

	// PatientID is the record the message concerned, when one could be read.
	PatientID string `gorm:"type:text;index" json:"patient_id,omitempty"`
	Outcome   string `gorm:"type:text;not null;index" json:"outcome"`
	AckCode   string `gorm:"type:text;not null" json:"ack_code"` // AA, AE, AR
	// Reason is what was sent back in MSA-3, or why nothing was applied.
	Reason string `gorm:"type:text" json:"reason,omitempty"`
}

func (HL7InboundMessage) TableName() string { return "hl7_inbound_messages" }
