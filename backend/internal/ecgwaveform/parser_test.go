package ecgwaveform

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"
)

// This package turns a DICOM waveform into the millivolt samples the WebGL
// viewer draws, and therefore into the on-screen scale the viewer's caliper
// measures against. It had no tests at all.
//
// The fixture is the one already committed for the DICOM connector; it is a
// synthetic recording, not patient data, so everything here runs in CI.

const fixture = "../connector/dicom/testdata/test_ecg.dcm"

func fixtureBytes(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(fixture))
	if err != nil {
		t.Fatalf("reading %s: %v", fixture, err)
	}
	return b
}

func TestParseFixture(t *testing.T) {
	rec, err := Parse(fixtureBytes(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if rec.SamplingFrequency != 500 {
		t.Errorf("sampling frequency = %v Hz, want 500", rec.SamplingFrequency)
	}
	if rec.DurationSec != 10 {
		t.Errorf("duration = %v s, want 10", rec.DurationSec)
	}
	if len(rec.Channels) != 12 {
		t.Fatalf("got %d channels, want 12", len(rec.Channels))
	}
	for i, want := range standardLeads12 {
		if rec.Channels[i].Label != want {
			t.Errorf("channel %d = %q, want %q", i, rec.Channels[i].Label, want)
		}
		if n := len(rec.Channels[i].Samples); n != 5000 {
			t.Errorf("channel %s has %d samples, want 5000", want, n)
		}
	}
}

// Checks the actual arithmetic end to end: take the first raw integer out of
// Waveform Data, apply the conversion the file itself declares, and require the
// decoded millivolt sample to match.
//
// A weaker version of this test compared samples against the declared
// quantisation step. That would have passed even with the unit misread, because
// a wrong scale factor cancels on both sides — it only proved the samples were
// multiples of something. This compares against an absolute value.
func TestDecodedSampleMatchesTheFilesOwnConversion(t *testing.T) {
	rec, err := Parse(fixtureBytes(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sens, corr, baseline, unitFactor := declaredChannel0(t)

	raw := firstRawSample(t)
	wantMv := float64(raw)*sens*corr*unitFactor + baseline*unitFactor

	leadName := rec.RawLeadOrder[0]
	var got float32
	found := false
	for _, ch := range rec.Channels {
		if ch.Label == leadName {
			got, found = ch.Samples[0], true
			break
		}
	}
	if !found {
		t.Fatalf("channel %q missing from the decoded record", leadName)
	}

	// float32 output, so compare with a float32-sized tolerance rather than
	// exactly; a unit or correction error is orders of magnitude larger.
	if math.Abs(float64(got)-wantMv) > 1e-6 {
		t.Errorf("lead %s sample 0 = %v mV, want %v mV (raw %d x %v x %v x %v + %v)",
			leadName, got, wantMv, raw, sens, corr, unitFactor, baseline)
	}

	// Guard the guard: if the unit had been read as mV instead of uV the value
	// would be off by a thousand, so make sure the tolerance cannot absorb it.
	if wrong := wantMv * 1000; math.Abs(float64(got)-wrong) <= 1e-6 && wantMv != 0 {
		t.Error("test cannot distinguish the correct scale from a 1000x error")
	}
}

// firstRawSample returns the first stored integer of the first channel.
func firstRawSample(t *testing.T) int16 {
	t.Helper()
	wf := waveformItem(t)
	elem, err := findInSeqItem(wf, tag.Tag{Group: 0x5400, Element: 0x1010})
	if err != nil {
		t.Fatalf("waveform data: %v", err)
	}
	data, ok := elem.Value.GetValue().([]byte)
	if !ok || len(data) < 2 {
		t.Fatal("waveform data is not usable bytes")
	}
	return int16(binary.LittleEndian.Uint16(data[:2]))
}

// declaredChannel0 reads the conversion parameters of the first channel out of
// the fixture, so the expectation comes from the file rather than from this
// code or from the parser under test.
func declaredChannel0(t *testing.T) (sens, corr, baseline, unitFactor float64) {
	t.Helper()
	chanDefs, err := getSeqItems(waveformItem(t), tag.Tag{Group: 0x003A, Element: 0x0200})
	if err != nil || len(chanDefs) == 0 {
		t.Fatalf("channel definition sequence: %v", err)
	}
	cd := chanDefs[0]

	if sens, err = getFloat64FromSeq(cd, tag.Tag{Group: 0x003A, Element: 0x0210}); err != nil {
		t.Fatalf("channel sensitivity: %v", err)
	}
	corr = 1.0
	if v, err := getFloat64FromSeq(cd, tag.Tag{Group: 0x003A, Element: 0x0212}); err == nil {
		corr = v
	}
	baseline = 0.0
	if v, err := getFloat64FromSeq(cd, tag.Tag{Group: 0x003A, Element: 0x0213}); err == nil {
		baseline = v
	}

	unitSeqs, err := getSeqItems(cd, tag.Tag{Group: 0x003A, Element: 0x0211})
	if err != nil || len(unitSeqs) == 0 {
		t.Fatalf("channel sensitivity units: %v", err)
	}
	code, err := getStringFromSeq(unitSeqs[0], tag.Tag{Group: 0x0008, Element: 0x0100})
	if err != nil {
		t.Fatalf("unit code: %v", err)
	}
	// Deliberately NOT sensitivityUnitToMv: deriving the expectation from the
	// function under test would make a wrong unit cancel on both sides, which
	// is exactly the bug this is here to catch.
	switch code {
	case "uV":
		unitFactor = 0.001
	case "mV":
		unitFactor = 1
	case "V":
		unitFactor = 1000
	default:
		t.Fatalf("fixture declares an unexpected sensitivity unit %q", code)
	}
	return sens, corr, baseline, unitFactor
}

// waveformItem returns the first item of the fixture's Waveform Sequence.
func waveformItem(t *testing.T) *dicom.SequenceItemValue {
	t.Helper()
	ds, err := dicom.ParseUntilEOF(bytes.NewReader(fixtureBytes(t)), nil)
	if err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}
	wf, err := ds.FindElementByTag(tag.Tag{Group: 0x5400, Element: 0x0100})
	if err != nil {
		t.Fatalf("waveform sequence: %v", err)
	}
	seqs, _ := wf.Value.GetValue().([]*dicom.SequenceItemValue)
	if len(seqs) == 0 {
		t.Fatal("empty waveform sequence")
	}
	return seqs[0]
}

// --- refusals --------------------------------------------------------------

// A file that announces more samples than it carries used to decode: the sample
// loop ran off the end of the buffer and left the rest of every channel at
// zero, drawing a flat line that reads as recorded asystole.
func TestParseRefusesTruncatedWaveformData(t *testing.T) {
	raw := fixtureBytes(t)
	ds, err := dicom.ParseUntilEOF(bytes.NewReader(raw), nil)
	if err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}
	if !halveWaveformData(t, &ds) {
		t.Skip("fixture layout does not expose Waveform Data for mutation")
	}

	var buf bytes.Buffer
	if err := dicom.Write(&buf, ds, dicom.SkipVRVerification(), dicom.SkipValueTypeVerification()); err != nil {
		t.Fatalf("rewriting mutated dataset: %v", err)
	}

	_, err = Parse(buf.Bytes())
	if !errors.Is(err, ErrTruncatedWaveform) {
		t.Fatalf("Parse error = %v, want ErrTruncatedWaveform", err)
	}
}

// halveWaveformData cuts the Waveform Data element in half in place.
func halveWaveformData(t *testing.T, ds *dicom.Dataset) bool {
	t.Helper()
	for _, elem := range ds.Elements {
		if elem.Tag != (tag.Tag{Group: 0x5400, Element: 0x0100}) {
			continue
		}
		seqs, ok := elem.Value.GetValue().([]*dicom.SequenceItemValue)
		if !ok || len(seqs) == 0 {
			return false
		}
		inners, ok := seqs[0].GetValue().([]*dicom.Element)
		if !ok {
			return false
		}
		for _, inner := range inners {
			if inner.Tag != (tag.Tag{Group: 0x5400, Element: 0x1010}) {
				continue
			}
			data, ok := inner.Value.GetValue().([]byte)
			if !ok || len(data) < 4 {
				return false
			}
			half := make([]byte, len(data)/2)
			copy(half, data[:len(half)])
			v, err := dicom.NewValue(half)
			if err != nil {
				return false
			}
			inner.Value = v
			inner.ValueLength = uint32(len(half))
			return true
		}
	}
	return false
}

// --- units -----------------------------------------------------------------

// The unit of Channel Sensitivity decides the scale of the whole trace. It used
// to default to mV, so a file in uV — the common case — was read a thousandfold
// too large, and a file stating no unit at all was read as if it had.
func TestSensitivityUnitToMv(t *testing.T) {
	for _, tc := range []struct {
		code string
		want float64
	}{
		{"uV", 0.001},
		{"µV", 0.001},
		{"uv", 0.001},
		{"mV", 1},
		{" mV ", 1},
		{"V", 1000},
	} {
		got, err := sensitivityUnitToMv(tc.code)
		if err != nil {
			t.Errorf("sensitivityUnitToMv(%q): unexpected error %v", tc.code, err)
			continue
		}
		if got != tc.want {
			t.Errorf("sensitivityUnitToMv(%q) = %v, want %v", tc.code, got, tc.want)
		}
	}
}

func TestSensitivityUnitToMvRefusesUnknown(t *testing.T) {
	for _, code := range []string{"", "   ", "mmHg", "counts", "LSB"} {
		if _, err := sensitivityUnitToMv(code); err == nil {
			t.Errorf("sensitivityUnitToMv(%q) accepted an unusable unit", code)
		}
	}
}

// --- lead derivation -------------------------------------------------------

func TestDeriveLeadFollowsEinthovenAndGoldberger(t *testing.T) {
	byLabel := map[string][]float32{
		"I":  {0.10, -0.04, 0},
		"II": {0.06, 0.02, -0.03},
	}
	n := 3

	for _, tc := range []struct {
		name string
		want func(i, ii float32) float32
	}{
		{"III", func(i, ii float32) float32 { return ii - i }},
		{"aVR", func(i, ii float32) float32 { return -(i + ii) * 0.5 }},
		{"aVL", func(i, ii float32) float32 { return i - ii*0.5 }},
		{"aVF", func(i, ii float32) float32 { return ii - i*0.5 }},
	} {
		got, err := deriveLead(tc.name, byLabel, n)
		if err != nil {
			t.Fatalf("deriveLead(%s): %v", tc.name, err)
		}
		for k := 0; k < n; k++ {
			want := tc.want(byLabel["I"][k], byLabel["II"][k])
			if math.Abs(float64(got[k]-want)) > 1e-6 {
				t.Errorf("%s[%d] = %v, want %v", tc.name, k, got[k], want)
			}
		}
	}
}

func TestDeriveLeadRefusesWithoutIAndII(t *testing.T) {
	if _, err := deriveLead("III", map[string][]float32{"I": {1}}, 1); err == nil {
		t.Error("deriving III without lead II was accepted")
	}
	if _, err := deriveLead("V3", map[string][]float32{"I": {1}, "II": {1}}, 1); err == nil {
		t.Error("a precordial lead was reported as derivable")
	}
}
