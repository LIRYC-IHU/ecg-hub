package hl7

import (
	"testing"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
)

func TestExtractByPath_PIDFields(t *testing.T) {
	// Standard HL7 PID segment with proper field positions:
	// PID-1=SetID, PID-2=(ext ID), PID-3=PatientID, PID-4=(alt ID), PID-5=Name,
	// PID-6=(mother maiden), PID-7=DOB, PID-8=Gender, PID-9=(alias), PID-10=(race),
	// PID-11=Address, PID-12=(county), PID-13=Phone
	raw := "MSH|^~\\&|APP|FAC|RECV|RFAC|20250101120000||ADR^A19|123|P|2.5\r" +
		"PID|1||P001||Dupont^Marie^J||19800315|F|||10 Rue de la Paix^Paris^^75001^FR||0612345678\r"

	tests := []struct {
		path string
		want string
	}{
		{"PID.5", "Dupont^Marie^J"},     // whole field (name)
		{"PID.5.1", "Dupont"},           // last name component
		{"PID.5.2", "Marie"},            // first name component
		{"PID.5.3", "J"},               // middle initial
		{"PID.7", "19800315"},          // DOB (whole field)
		{"PID.8", "F"},                 // gender
		{"PID.11", "10 Rue de la Paix^Paris^^75001^FR"}, // address whole field
		{"PID.11.1", "10 Rue de la Paix"},               // street
		{"PID.11.2", "Paris"},                           // city
		{"PID.11.4", "75001"},                           // zip
		{"PID.13", "0612345678"},       // phone
		{"PID.3", "P001"},             // patient ID
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := ExtractByPath(raw, tc.path)
			if got != tc.want {
				t.Errorf("ExtractByPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestExtractByPath_MSHFields(t *testing.T) {
	raw := "MSH|^~\\&|APP|FAC|RECV|RFAC|20250101120000||ADR^A19|123|P|2.5\r" +
		"PID|||P001||Dupont^Marie|||19800315|F\r"

	tests := []struct {
		path string
		want string
	}{
		{"MSH.1", "|"},        // field separator
		{"MSH.2", "^~\\&"},   // encoding characters
		{"MSH.3", "APP"},     // sending application
		{"MSH.4", "FAC"},     // sending facility
		{"MSH.5", "RECV"},    // receiving application
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := ExtractByPath(raw, tc.path)
			if got != tc.want {
				t.Errorf("ExtractByPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestExtractByPath_NotFound(t *testing.T) {
	raw := "MSH|^~\\&|APP|FAC\rPID|||P001||Dupont^Marie|||19800315|F\r"

	tests := []struct {
		name string
		path string
	}{
		{"missing segment", "OBX.1"},
		{"field out of range", "PID.99"},
		{"component out of range", "PID.5.99"},
		{"invalid path no dot", "PID"},
		{"invalid field index", "PID.abc"},
		{"invalid component index", "PID.5.abc"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractByPath(raw, tc.path)
			if got != "" {
				t.Errorf("ExtractByPath(%q) = %q, want empty string", tc.path, got)
			}
		})
	}
}

func TestApplyMappings(t *testing.T) {
	raw := "MSH|^~\\&|APP|FAC|RECV|RFAC|20250101120000||ADR^A19|123|P|2.5\r" +
		"PID|1||P001||Dupont^Marie||19800315|F|||10 Rue de la Paix^Paris^^75001^FR||0612345678\r"

	mappings := []models.HL7Mapping{
		{SourcePath: "PID.5.1", TargetField: "last_name"},
		{SourcePath: "PID.5.2", TargetField: "first_name"},
		{SourcePath: "PID.7", TargetField: "date_of_birth"},
		{SourcePath: "PID.8", TargetField: "gender"},
		{SourcePath: "PID.11.1", TargetField: "address"},
		{SourcePath: "PID.13", TargetField: "phone"},
	}

	d := ApplyMappings(raw, mappings)

	if d.LastName != "Dupont" {
		t.Errorf("LastName = %q, want %q", d.LastName, "Dupont")
	}
	if d.FirstName != "Marie" {
		t.Errorf("FirstName = %q, want %q", d.FirstName, "Marie")
	}
	if d.DateOfBirth != "19800315" {
		t.Errorf("DateOfBirth = %q, want %q", d.DateOfBirth, "19800315")
	}
	if d.Gender != "F" {
		t.Errorf("Gender = %q, want %q", d.Gender, "F")
	}
	if d.Address != "10 Rue de la Paix" {
		t.Errorf("Address = %q, want %q", d.Address, "10 Rue de la Paix")
	}
	if d.Phone != "0612345678" {
		t.Errorf("Phone = %q, want %q", d.Phone, "0612345678")
	}
}

func TestApplyMappings_EmptyMappings(t *testing.T) {
	raw := "PID|||P001||Dupont^Marie|||19800315|F\r"
	d := ApplyMappings(raw, nil)
	if d.LastName != "" || d.FirstName != "" || d.DateOfBirth != "" {
		t.Errorf("expected empty demographics with nil mappings, got %+v", d)
	}
}
