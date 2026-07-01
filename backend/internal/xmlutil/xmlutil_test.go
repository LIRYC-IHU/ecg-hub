package xmlutil

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
)

// windows1252Doc builds a minimal RestingECG document declared as Windows-1252
// containing a byte (0xE9 = "é") that is invalid as standalone UTF-8, mirroring
// real GE MUSE exports.
func windows1252Doc() []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="Windows-1252"?>` + "\n")
	b.WriteString(`<RestingECG><PatientDemographics><PatientLastName>`)
	b.WriteByte(0xE9) // 'é' in Windows-1252
	b.WriteString(`</PatientLastName></PatientDemographics></RestingECG>`)
	return b.Bytes()
}

func TestRootElement_Windows1252(t *testing.T) {
	name, err := RootElement(windows1252Doc())
	if err != nil {
		t.Fatalf("RootElement returned error on Windows-1252 doc: %v", err)
	}
	if name.Local != "RestingECG" {
		t.Fatalf("root = %q, want RestingECG", name.Local)
	}
}

func TestUnmarshal_Windows1252(t *testing.T) {
	var v struct {
		XMLName  xml.Name `xml:"RestingECG"`
		LastName string   `xml:"PatientDemographics>PatientLastName"`
	}
	if err := Unmarshal(windows1252Doc(), &v); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if v.LastName != "é" {
		t.Fatalf("LastName = %q, want é (charset not decoded)", v.LastName)
	}
}

func TestToUTF8_Windows1252(t *testing.T) {
	out, err := ToUTF8(windows1252Doc())
	if err != nil {
		t.Fatalf("ToUTF8 error: %v", err)
	}
	if bytes.Contains(out, []byte{0xE9}) {
		t.Fatalf("output still contains raw 0xE9 byte; not transcoded to UTF-8")
	}
	if !strings.Contains(string(out), `encoding="UTF-8"`) {
		t.Fatalf("declaration not rewritten to UTF-8: %s", out[:60])
	}
	// Resulting bytes must now be parseable by the stdlib UTF-8-only decoder.
	var v struct {
		LastName string `xml:"PatientDemographics>PatientLastName"`
	}
	if err := xml.Unmarshal(out, &v); err != nil {
		t.Fatalf("stdlib xml.Unmarshal failed on transcoded output: %v", err)
	}
	if v.LastName != "é" {
		t.Fatalf("LastName = %q, want é", v.LastName)
	}
}

func TestToUTF8_PassThroughUTF8(t *testing.T) {
	in := []byte(`<?xml version="1.0" encoding="UTF-8"?><RestingECG/>`)
	out, err := ToUTF8(in)
	if err != nil {
		t.Fatalf("ToUTF8 error: %v", err)
	}
	if !bytes.Equal(in, out) {
		t.Fatalf("UTF-8 input mutated: %s", out)
	}
}

func TestToUTF8_NoDeclaration(t *testing.T) {
	in := []byte(`<RestingECG/>`)
	out, err := ToUTF8(in)
	if err != nil {
		t.Fatalf("ToUTF8 error: %v", err)
	}
	if !bytes.Equal(in, out) {
		t.Fatalf("declaration-less input mutated: %s", out)
	}
}
