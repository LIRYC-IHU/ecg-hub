package philips_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LIRYC-IHU/ecg-hub/internal/module/philips"
)

const minimalXML = `<?xml version="1.0"?>
<restingecgdata xmlns="http://www3.medical.philips.com" status="New">
  <documentinfo>
    <documenttype>SierraECG</documenttype>
    <documentversion>1.03</documentversion>
  </documentinfo>
  <dataacquisition date="2025-07-21" time="10:41:44" statflag="False">
    <machine machineid="" detaildescription="Philips Medical Products:860306:A.07.07.07">PageWriter</machine>
    <signalcharacteristics>
      <samplingrate>500</samplingrate>
    </signalcharacteristics>
  </dataacquisition>
  <reportinfo date="2025-07-21" time="10:41:44">
    <reportformat>
      <waveformformat>
        <mainwaveformformat nrow="3" ncolumn="4">I II III aVR aVL aVF V1 V2 V3 V4 V5 V6</mainwaveformformat>
      </waveformformat>
    </reportformat>
  </reportinfo>
  <patient>
    <generalpatientdata>
      <patientid>07071980</patientid>
      <name>
        <lastname>MANSFIELD</lastname>
        <firstname>CECILE</firstname>
      </name>
      <sex>Female</sex>
    </generalpatientdata>
  </patient>
  <waveforms>
    <parsedwaveforms compressflag="True" dataencoding="Base64" durationperchannel="11000" nbitspersample="16">AAAA</parsedwaveforms>
  </waveforms>
</restingecgdata>`

func newModule() *philips.Module {
	return &philips.Module{}
}

func TestModule_Name(t *testing.T) {
	if got := newModule().Name(); got != "philips" {
		t.Errorf("Name() = %q, want %q", got, "philips")
	}
}

func TestModule_AcceptedExtensions(t *testing.T) {
	exts := newModule().AcceptedExtensions()
	for _, e := range exts {
		if e == ".xml" {
			return
		}
	}
	t.Errorf("AcceptedExtensions() does not contain .xml, got %v", exts)
}

func TestModule_Health(t *testing.T) {
	if err := newModule().Health(); err != nil {
		t.Errorf("Health() unexpected error: %v", err)
	}
}

func TestModule_Validate_Valid(t *testing.T) {
	if err := newModule().Validate([]byte(minimalXML)); err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestModule_Validate_Empty(t *testing.T) {
	if err := newModule().Validate([]byte{}); err == nil {
		t.Error("Validate() with empty data should return an error")
	}
}

func TestModule_Parse_ValidXML(t *testing.T) {
	meta, err := newModule().Parse(context.Background(), []byte(minimalXML))
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if meta == nil {
		t.Fatal("Parse() returned nil ECGMetadata")
	}
	if meta.PatientID != "07071980" {
		t.Errorf("PatientID = %q, want %q", meta.PatientID, "07071980")
	}
	if meta.VendorName != "philips" {
		t.Errorf("VendorName = %q, want %q", meta.VendorName, "philips")
	}
	if meta.SourceFormat != "philips_sierraecg_xml" {
		t.Errorf("SourceFormat = %q, want %q", meta.SourceFormat, "philips_sierraecg_xml")
	}
	if meta.DeviceModel != "Philips Medical Products:860306:A.07.07.07" {
		t.Errorf("DeviceModel = %q", meta.DeviceModel)
	}
	if meta.LeadCount != 12 {
		t.Errorf("LeadCount = %d, want 12 (nrow=3 × ncol=4)", meta.LeadCount)
	}
	if meta.DurationSeconds != 11.0 {
		t.Errorf("DurationSeconds = %f, want 11.0", meta.DurationSeconds)
	}
	if meta.SampleRate != 500.0 {
		t.Errorf("SampleRate = %f, want 500.0", meta.SampleRate)
	}
	want := time.Date(2025, 7, 21, 10, 41, 44, 0, time.UTC)
	if !meta.RecordedAt.Equal(want) {
		t.Errorf("RecordedAt = %v, want %v", meta.RecordedAt, want)
	}
	if got, ok := meta.Extra["last_name"]; !ok || got != "MANSFIELD" {
		t.Errorf("Extra[last_name] = %v, want MANSFIELD", got)
	}
}

func TestModule_Parse_EmptyData(t *testing.T) {
	_, err := newModule().Parse(context.Background(), []byte{})
	if err == nil {
		t.Error("Parse() with empty data should return an error")
	}
}

func TestModule_Parse_MissingPatientID(t *testing.T) {
	xml := strings.ReplaceAll(minimalXML, "<patientid>07071980</patientid>", "<patientid></patientid>")
	_, err := newModule().Parse(context.Background(), []byte(xml))
	if err == nil {
		t.Error("Parse() with missing patientid should return an error")
	}
}

func TestModule_Parse_InvalidXML(t *testing.T) {
	_, err := newModule().Parse(context.Background(), []byte("not xml at all"))
	if err == nil {
		t.Error("Parse() with invalid XML should return an error")
	}
}

func TestModule_RenamePatientID(t *testing.T) {
	updated, err := newModule().RenamePatientID([]byte(minimalXML), "NEW-ID-999")
	if err != nil {
		t.Fatalf("RenamePatientID() unexpected error: %v", err)
	}
	if !strings.Contains(string(updated), "NEW-ID-999") {
		t.Error("RenamePatientID() result does not contain new patient ID")
	}
	if strings.Contains(string(updated), "07071980") {
		t.Error("RenamePatientID() result still contains old patient ID")
	}
}

func TestModule_RenamePatientID_EmptyID(t *testing.T) {
	_, err := newModule().RenamePatientID([]byte(minimalXML), "")
	if err == nil {
		t.Error("RenamePatientID() with empty newID should return an error")
	}
}
