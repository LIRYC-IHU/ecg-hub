package ecgwaveform

import (
	"bytes"
	"errors"
	"testing"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"
)

// A well-formed waveform the 12-lead viewer cannot draw is refused with its own
// typed error — not the generic parse failure the UI used to show (#130 D-13).

const hemodynamicUID = "1.2.840.10008.5.1.4.1.1.9.2.1"

// mutateFixture parses the fixture, applies edit to every Channel Definition
// item and sets the SOP Class UID, then re-encodes it.
func mutateFixture(t *testing.T, sopClass string, edit func(i int, cd *dicom.SequenceItemValue)) []byte {
	t.Helper()
	ds, err := dicom.ParseUntilEOF(bytes.NewReader(fixtureBytes(t)), nil)
	if err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}
	for _, e := range ds.Elements {
		if e.Tag == tag.SOPClassUID {
			v, _ := dicom.NewValue([]string{sopClass})
			e.Value = v
		}
	}
	defs, err := getSeqItems(waveformOf(t, &ds), tag.Tag{Group: 0x003A, Element: 0x0200})
	if err != nil {
		t.Fatalf("channel definitions: %v", err)
	}
	for i, cd := range defs {
		edit(i, cd)
	}
	var buf bytes.Buffer
	if err := dicom.Write(&buf, ds, dicom.SkipVRVerification(), dicom.SkipValueTypeVerification()); err != nil {
		t.Fatalf("rewriting mutated dataset: %v", err)
	}
	return buf.Bytes()
}

func waveformOf(t *testing.T, ds *dicom.Dataset) *dicom.SequenceItemValue {
	t.Helper()
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

// setInSeq replaces the first string of tag t inside the first item of the
// sequence seqTag of cd.
func setInSeq(t *testing.T, cd *dicom.SequenceItemValue, seqTag, tg tag.Tag, value string) {
	t.Helper()
	items, err := getSeqItems(cd, seqTag)
	if err != nil || len(items) == 0 {
		t.Fatalf("sequence %v: %v", seqTag, err)
	}
	e, err := findInSeqItem(items[0], tg)
	if err != nil {
		t.Fatalf("tag %v: %v", tg, err)
	}
	v, _ := dicom.NewValue([]string{value})
	e.Value = v
}

func notDisplayable(t *testing.T, data []byte) *NotDisplayableError {
	t.Helper()
	_, err := Parse(data)
	var nd *NotDisplayableError
	if !errors.As(err, &nd) || !errors.Is(err, ErrNotDisplayable) {
		t.Fatalf("Parse error = %v, want a *NotDisplayableError", err)
	}
	return nd
}

// Channels that are not the standard leads — an ambulatory or hemodynamic
// recording — cannot fill a 12-lead layout.
func TestParse_NotDisplayableWhenTheStandardLeadsAreAbsent(t *testing.T) {
	data := mutateFixture(t, hemodynamicUID, func(i int, cd *dicom.SequenceItemValue) {
		setInSeq(t, cd, tag.Tag{Group: 0x003A, Element: 0x0208}, tag.Tag{Group: 0x0008, Element: 0x0104}, "Pressure")
	})
	nd := notDisplayable(t, data)
	if nd.Reason != ReasonLeadsMissing {
		t.Errorf("reason = %q, want %q", nd.Reason, ReasonLeadsMissing)
	}
	if nd.SOPClassUID != hemodynamicUID {
		t.Errorf("SOP class = %q, want %q", nd.SOPClassUID, hemodynamicUID)
	}
}

// A pressure in mmHg drawn on an mm/mV grid would be read as a voltage.
func TestParse_NotDisplayableWhenAChannelIsNotAVoltage(t *testing.T) {
	data := mutateFixture(t, hemodynamicUID, func(i int, cd *dicom.SequenceItemValue) {
		if i == 0 {
			setInSeq(t, cd, tag.Tag{Group: 0x003A, Element: 0x0211}, tag.Tag{Group: 0x0008, Element: 0x0100}, "mm[Hg]")
		}
	})
	nd := notDisplayable(t, data)
	if nd.Reason != ReasonNonElectricalUnit {
		t.Errorf("reason = %q, want %q", nd.Reason, ReasonNonElectricalUnit)
	}
}

// An absent unit is still a malformed file, not "another kind of signal".
func TestParse_AbsentUnitStaysAMissingParameter(t *testing.T) {
	data := mutateFixture(t, hemodynamicUID, func(i int, cd *dicom.SequenceItemValue) {
		if i == 0 {
			setInSeq(t, cd, tag.Tag{Group: 0x003A, Element: 0x0211}, tag.Tag{Group: 0x0008, Element: 0x0100}, "")
		}
	})
	_, err := Parse(data)
	if !errors.Is(err, ErrMissingAcquisitionParameter) || errors.Is(err, ErrNotDisplayable) {
		t.Fatalf("Parse error = %v, want ErrMissingAcquisitionParameter only", err)
	}
}

// The SOP class does not gate display: a non-12-lead class carrying the 12
// standard leads in volts is drawn.
func TestParse_DrawsAnyClassThatCarriesTheTwelveLeads(t *testing.T) {
	data := mutateFixture(t, "1.2.840.10008.5.1.4.1.1.9.1.2", func(int, *dicom.SequenceItemValue) {})
	if _, err := Parse(data); err != nil {
		t.Fatalf("General ECG with the 12 leads refused: %v", err)
	}
}
