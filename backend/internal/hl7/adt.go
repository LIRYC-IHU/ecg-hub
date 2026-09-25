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
			u.PatientID = firstComponent(f.Value)
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

// defaultPriorPatientIDPath is where HL7 puts the identifier being merged away.
// MRG-1 is fixed by the standard rather than chosen by a site, so it is a
// default rather than a required mapping — the same treatment MSA.1 and MSA.3
// get on the query path. A site whose feed puts it elsewhere can still map
// "prior_patient_id".
const defaultPriorPatientIDPath = "MRG.1"

// PatientMerge is what an A40 asks for: two records found to be the same person.
//
// The direction is the whole of it, and it is easy to read backwards. PID-3
// carries the surviving identifier — "the dominant patient information", in the
// framework's words — and MRG-1 the one to stop referencing.
type PatientMerge struct {
	// SurvivingID is PID-3: the identifier that remains in use.
	SurvivingID string
	// PriorID is MRG-1: the identifier to stop referencing.
	PriorID string
	// Source identifies the sender.
	Source string
}

// BuildPatientMerge reads the two identifiers an A40 carries.
func BuildPatientMerge(msg *InboundMessage, mappings []models.HL7Mapping) *PatientMerge {
	m := &PatientMerge{Source: msg.SendingFacility}

	priorPath := defaultPriorPatientIDPath
	for _, mp := range mappings {
		switch mp.TargetField {
		case "patient_id":
			m.SurvivingID = firstComponent(ExtractByPath(msg.Raw, mp.SourcePath))
		case "prior_patient_id":
			priorPath = mp.SourcePath
		}
	}
	m.PriorID = firstComponent(ExtractByPath(msg.Raw, priorPath))
	return m
}

// firstComponent returns the identifier out of a CX, which is its first
// component whatever else the field carries — an assigning authority, a type
// code. Accepts a mapping written as PID.3 or PID.3.1 without making a site pick.
func firstComponent(v string) string {
	return strings.TrimSpace(strings.SplitN(v, "^", 2)[0])
}

// PatientMerger moves a patient onto the surviving identifier.
type PatientMerger interface {
	// RekeyPatient renames the record when the surviving identifier is free and
	// merges into it when it is taken, reporting how many ECGs changed hands.
	RekeyPatient(oldID, newID string) (int64, error)
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
	return newADTHandler(repo, nil, mappings)
}

// NewADTHandler returns the listener handler for both messages this system acts
// on: A08 updates the demographics of a record, A40 merges two records.
//
// They are separate on purpose, and the framework insists on it: an A08 may not
// change a patient identifier, and an A40 is the only message that may. Passing
// merger as nil leaves A40 acknowledged and unapplied.
func NewADTHandler(
	updates PatientUpdateApplier,
	merger PatientMerger,
	mappings func() ([]models.HL7Mapping, error),
) Handler {
	return newADTHandler(updates, merger, mappings)
}

func newADTHandler(
	repo PatientUpdateApplier,
	merger PatientMerger,
	mappings func() ([]models.HL7Mapping, error),
) Handler {
	return func(msg *InboundMessage) (string, string) {
		ack := func(code, text string) (string, string) {
			appmetrics.HL7InboundHandled.WithLabelValues(msg.TriggerEvent, code).Inc()
			return code, text
		}

		if msg.TriggerEvent != "A08" && !(msg.TriggerEvent == "A40" && merger != nil) {
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

		if msg.TriggerEvent == "A40" {
			return applyMerge(msg, active, merger, ack)
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

// applyMerge carries out an A40.
//
// A replay is harmless without any extra machinery: once the prior identifier
// has been merged away there is no record under it, and the merge becomes a no
// operation. That is why this path needs none of the staleness checking an A08
// does — there, a replayed message would put an old name back.
func applyMerge(
	msg *InboundMessage,
	mappings []models.HL7Mapping,
	merger PatientMerger,
	ack func(string, string) (string, string),
) (string, string) {
	m := BuildPatientMerge(msg, mappings)

	if m.SurvivingID == "" || m.PriorID == "" {
		slog.Warn("hl7 adt: a merge needs both identifiers",
			"surviving", m.SurvivingID, "prior", m.PriorID, "control_id", msg.ControlID)
		return ack(ACKError, "a merge needs both PID-3 and MRG-1")
	}
	if m.SurvivingID == m.PriorID {
		slog.Info("hl7 adt: merge into itself — nothing to do",
			"patient_id", m.SurvivingID, "control_id", msg.ControlID)
		return ack(ACKAccepted, "")
	}

	moved, err := merger.RekeyPatient(m.PriorID, m.SurvivingID)
	if err != nil {
		slog.Error("hl7 adt: merge failed",
			"prior", m.PriorID, "surviving", m.SurvivingID,
			"control_id", msg.ControlID, "error", err)
		return ack(ACKError, "the merge could not be applied")
	}

	// The framework says to create the surviving patient from the A40 when the
	// prior one is unknown. This system does not: its patients are created when
	// an ECG arrives, and a merge about two people it has never seen concerns it
	// no more than an update about one. Accepted rather than refused, for the
	// same reason — the sender has nothing to fix by retrying.
	slog.Info("hl7 adt: patient merged",
		"prior", m.PriorID, "surviving", m.SurvivingID,
		"ecgs_moved", moved, "facility", m.Source, "control_id", msg.ControlID)
	return ack(ACKAccepted, "")
}
