package hl7

import (
	"log/slog"
	"strings"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	appmetrics "github.com/LIRYC-IHU/ecg-hub/internal/metrics"
)

// hl7Null is how HL7 says "remove this value": two double quotes, as opposed to
// an omitted field, which means leave what is stored alone. RAD-12 §4.12.4.3.2
// makes the distinction explicit for A08, and it is the reason an update carries
// Field values rather than plain strings.
const hl7Null = `""`

// Field is one value from an inbound message, with the three states an HL7
// update can express.
type Field struct {
	Value   string
	Present bool // the message carried this field at all
	Clear   bool // it carried it as "" — remove the stored value
}

// Set reports whether the field names a value to store.
func (f Field) Set() bool { return f.Present && !f.Clear }

// PatientUpdate is what an A08 asks to change. Every field is three-state, so
// applying it can tell "leave alone" from "erase".
type PatientUpdate struct {
	// PatientID is the record to update, never a value to write: RAD-12 is
	// explicit that an A08 cannot change a patient identifier, and that an A40
	// is the only message that may.
	PatientID string

	LastName    Field
	FirstName   Field
	DateOfBirth Field
	Gender      Field
	NDA         Field

	// EventAt is when the sending system recorded the change — EVN-2 when the
	// message carries it, MSH-7 otherwise. Used to ignore a message older than
	// what has already been applied.
	EventAt time.Time
	// Source identifies the sender, stored as the provenance of the update.
	Source string
}

// Empty reports whether the message asked for no change at all.
func (u *PatientUpdate) Empty() bool {
	for _, f := range []Field{u.LastName, u.FirstName, u.DateOfBirth, u.Gender, u.NDA} {
		if f.Present {
			return false
		}
	}
	return true
}

// field reads one mapped path into a Field.
func field(raw, path string) Field {
	v, present := ExtractField(raw, path)
	if !present {
		return Field{}
	}
	if strings.TrimSpace(v) == hl7Null {
		return Field{Present: true, Clear: true}
	}
	return Field{Value: strings.TrimSpace(v), Present: true}
}

// BuildPatientUpdate turns an inbound message into the change it asks for, using
// the site's configured field mappings — the same ones the query path uses,
// because an ADT carries the same PID segment a query response does.
func BuildPatientUpdate(msg *InboundMessage, mappings []models.HL7Mapping) *PatientUpdate {
	u := &PatientUpdate{Source: msg.SendingFacility}

	for _, m := range mappings {
		f := field(msg.Raw, m.SourcePath)
		switch m.TargetField {
		case "patient_id":
			// PID-3 is a CX, so its first component is the identifier whatever
			// else the field carries — an assigning authority, a type code. This
			// accepts a mapping written as PID.3 or as PID.3.1 without making a
			// site pick.
			u.PatientID = strings.TrimSpace(strings.SplitN(f.Value, "^", 2)[0])
		case "last_name":
			u.LastName = f
		case "first_name":
			u.FirstName = f
		case "date_of_birth":
			u.DateOfBirth = f
		case "gender":
			u.Gender = f
		case "nda":
			u.NDA = f
		}
	}

	u.EventAt = eventTime(msg.Raw)
	return u
}

// eventTime reads when the sending system says the change happened.
//
// EVN-2 is preferred over MSH-7: it is when the event was recorded, where MSH-7
// is merely when this copy of the message was built — and a retransmission
// rebuilds the header while the event stays where it was.
func eventTime(raw string) time.Time {
	for _, path := range []string{"EVN.2", "MSH.7"} {
		if v, ok := ExtractField(raw, path); ok {
			if t, parsed := parseHL7Time(strings.TrimSpace(v)); parsed {
				return t
			}
		}
	}
	return time.Time{}
}

// parseHL7Time reads an HL7 timestamp, with or without the offset a sender may
// include. A value with no offset is read as UTC: it is compared only against
// another timestamp from the same feed, so a consistent reading matters more
// than a correct one.
func parseHL7Time(v string) (time.Time, bool) {
	for _, layout := range []string{
		"20060102150405-0700",
		"200601021504-0700",
		"20060102150405",
		"200601021504",
		"20060102",
	} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// PatientUpdateApplier applies an A08 to the stored record.
type PatientUpdateApplier interface {
	// ApplyPatientUpdate writes the fields the message carried and clears the
	// ones it nulled. It reports whether a patient was found; an unknown patient
	// is not an error.
	ApplyPatientUpdate(u *PatientUpdate) (found bool, err error)
}

// NewPatientUpdateHandler returns the listener handler that applies A08 messages.
//
// Anything else is acknowledged and left alone. RAD-12 also defines A02, A03,
// A06 and A07, which carry visit and location information this system stores
// nowhere, and A40, which is a merge and belongs to its own path — none of them
// is an error, so none of them earns a rejection that would make a sender retry.
func NewPatientUpdateHandler(repo PatientUpdateApplier, mappings func() ([]models.HL7Mapping, error)) Handler {
	return func(msg *InboundMessage) (string, string) {
		ack := func(code, text string) (string, string) {
			appmetrics.HL7InboundHandled.WithLabelValues(msg.TriggerEvent, code).Inc()
			return code, text
		}

		if msg.TriggerEvent != "A08" {
			slog.Info("hl7 adt: acknowledged without acting",
				"trigger", msg.TriggerEvent, "control_id", msg.ControlID)
			return ack(ACKAccepted, "")
		}

		active, err := mappings()
		if err != nil {
			slog.Error("hl7 adt: could not load the field mappings", "error", err)
			return ack(ACKError, "field mappings unavailable")
		}
		if len(active) == 0 {
			// Without mappings nothing can be read out of the message, and
			// applying an empty update would blank the record.
			slog.Error("hl7 adt: no active field mapping preset — refusing to interpret the message")
			return ack(ACKError, "no active field mapping preset")
		}

		u := BuildPatientUpdate(msg, active)
		if u.PatientID == "" {
			slog.Warn("hl7 adt: no patient identifier in the message",
				"control_id", msg.ControlID, "facility", msg.SendingFacility)
			return ack(ACKError, "no patient identifier could be read from PID-3")
		}
		if u.Empty() {
			slog.Info("hl7 adt: nothing to change", "patient_id", u.PatientID, "control_id", msg.ControlID)
			return ack(ACKAccepted, "")
		}

		found, err := repo.ApplyPatientUpdate(u)
		if err != nil {
			slog.Error("hl7 adt: update failed",
				"patient_id", u.PatientID, "control_id", msg.ControlID, "error", err)
			return ack(ACKError, "the update could not be applied")
		}
		if !found {
			// Patients here are created when an ECG arrives. Creating one from
			// an ADT feed would fill the table with the whole hospital, for
			// people this system will never hold a trace for. Accepted, because
			// the message is valid and simply does not concern us — a rejection
			// would have the sender retry something that will never apply.
			slog.Info("hl7 adt: no such patient — ignored",
				"patient_id", u.PatientID, "control_id", msg.ControlID)
			return ack(ACKAccepted, "")
		}

		slog.Info("hl7 adt: patient updated",
			"patient_id", u.PatientID, "control_id", msg.ControlID,
			"facility", msg.SendingFacility, "event_at", u.EventAt)
		return ack(ACKAccepted, "")
	}
}
