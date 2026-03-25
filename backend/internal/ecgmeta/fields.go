// Package ecgmeta defines the editable metadata fields for ECG records
// and the generic interface for format-specific file updates.
package ecgmeta

// FieldType is the input type hint sent to the frontend.
type FieldType string

const (
	FieldTypeText     FieldType = "text"
	FieldTypeNumber   FieldType = "number"
	FieldTypeDatetime FieldType = "datetime"
	FieldTypeSelect   FieldType = "select"
)

// EditableField describes a single metadata field that can be read and modified.
type EditableField struct {
	Label   string    // human-readable label (used by frontend)
	Type    FieldType // input type hint
	Options []string  // non-nil only for FieldTypeSelect
}

// EditableFields is the authoritative list of ECG metadata fields that the API
// accepts for reading and writing. Add or remove entries here to control which
// fields are exposed — no other file needs changing.
//
// Key convention: snake_case, matches the JSON key in extra JSONB and the API body.
// Special key "recorded_at" maps to the dedicated ecgs.recorded_at column in addition
// to being mirrored in extra for uniform handling.
var EditableFields = map[string]EditableField{
	"recorded_at":      {Label: "Date d'enregistrement", Type: FieldTypeDatetime},
	"last_name":        {Label: "Nom", Type: FieldTypeText},
	"first_name":       {Label: "Prénom", Type: FieldTypeText},
	"sex":              {Label: "Sexe", Type: FieldTypeSelect, Options: []string{"M", "F", "U"}},
	"device_model":     {Label: "Modèle appareil", Type: FieldTypeText},
	"lead_count":       {Label: "Dérivations", Type: FieldTypeNumber},
	"duration_seconds": {Label: "Durée (s)", Type: FieldTypeNumber},
	"sample_rate":      {Label: "Fréquence (Hz)", Type: FieldTypeNumber},
	"document_type":    {Label: "Type document", Type: FieldTypeText},
	"document_version": {Label: "Version document", Type: FieldTypeText},
}

// FieldDef is the wire format for a single field definition sent to the frontend.
type FieldDef struct {
	Key     string    `json:"key"`
	Label   string    `json:"label"`
	Type    FieldType `json:"type"`
	Options []string  `json:"options,omitempty"`
}

// FieldList returns the sorted list of EditableField definitions for the API response.
func FieldList() []FieldDef {
	// Fixed order for stable API output.
	order := []string{
		"recorded_at", "last_name", "first_name", "sex",
		"device_model", "lead_count", "duration_seconds", "sample_rate",
		"document_type", "document_version",
	}
	out := make([]FieldDef, 0, len(order))
	for _, k := range order {
		if f, ok := EditableFields[k]; ok {
			out = append(out, FieldDef{Key: k, Label: f.Label, Type: f.Type, Options: f.Options})
		}
	}
	return out
}
