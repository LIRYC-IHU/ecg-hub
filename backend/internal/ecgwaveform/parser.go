// Package ecgwaveform parses DICOM Resting 12-Lead ECG files (SOP 1.2.840.10008.5.1.4.1.1.9.1.1)
// and encodes the result in the binary wire format expected by the ECG viewer component.
package ecgwaveform

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"
)

// EcgChannel holds one lead's samples in mV.
type EcgChannel struct {
	Label   string
	Samples []float32
}

// EcgRecord is the parsed ECG record.
type EcgRecord struct {
	Channels          []EcgChannel
	SamplingFrequency float64
	DurationSec       float64
	PatientName       string
	AcquisitionDate   string
	RawLeadOrder      []string
}

// standardLeads12 is the required output order.
var standardLeads12 = []string{"I", "II", "III", "aVR", "aVL", "aVF", "V1", "V2", "V3", "V4", "V5", "V6"}

// Parse parses DICOM ECG bytes and returns an EcgRecord.
func Parse(data []byte) (*EcgRecord, error) {
	ds, err := dicom.ParseUntilEOF(bytes.NewReader(data), nil)
	if err != nil {
		return nil, fmt.Errorf("ecgwaveform: parse dicom: %w", err)
	}

	// Patient name
	patientName := ""
	if elem, err := ds.FindElementByTag(tag.PatientName); err == nil {
		if vals, ok := elem.Value.GetValue().([]string); ok && len(vals) > 0 {
			patientName = vals[0]
		}
	}

	// Acquisition date
	acquisitionDate := ""
	if elem, err := ds.FindElementByTag(tag.ContentDate); err == nil {
		if vals, ok := elem.Value.GetValue().([]string); ok && len(vals) > 0 {
			acquisitionDate = vals[0]
		}
	}

	// Waveform Sequence (5400,0100)
	waveformSeqTag := tag.Tag{Group: 0x5400, Element: 0x0100}
	waveformSeqElem, err := ds.FindElementByTag(waveformSeqTag)
	if err != nil {
		return nil, fmt.Errorf("ecgwaveform: waveform sequence (5400,0100) not found")
	}

	seqs, ok := waveformSeqElem.Value.GetValue().([]*dicom.SequenceItemValue)
	if !ok || len(seqs) == 0 {
		return nil, fmt.Errorf("ecgwaveform: empty waveform sequence")
	}

	waveform := seqs[0]

	// Number of waveform channels (003A,0005)
	numChannels, err := getUint16FromSeq(waveform, tag.Tag{Group: 0x003A, Element: 0x0005})
	if err != nil {
		return nil, fmt.Errorf("ecgwaveform: number of channels: %w", err)
	}

	// Number of waveform samples (003A,0010)
	numSamples, err := getUint32FromSeq(waveform, tag.Tag{Group: 0x003A, Element: 0x0010})
	if err != nil {
		return nil, fmt.Errorf("ecgwaveform: number of samples: %w", err)
	}

	// Sampling frequency (003A,001A)
	samplingFreqStr, err := getStringFromSeq(waveform, tag.Tag{Group: 0x003A, Element: 0x001A})
	if err != nil {
		return nil, fmt.Errorf("ecgwaveform: sampling frequency: %w", err)
	}
	var samplingFreq float64
	fmt.Sscanf(strings.TrimSpace(samplingFreqStr), "%f", &samplingFreq)
	if samplingFreq == 0 {
		return nil, fmt.Errorf("ecgwaveform: invalid sampling frequency %q", samplingFreqStr)
	}

	// Bits allocated (5400,1004)
	bitsAllocated, err := getUint16FromSeq(waveform, tag.Tag{Group: 0x5400, Element: 0x1004})
	if err != nil {
		return nil, fmt.Errorf("ecgwaveform: bits allocated: %w", err)
	}
	if bitsAllocated != 8 && bitsAllocated != 16 {
		return nil, fmt.Errorf("ecgwaveform: unsupported bits allocated %d", bitsAllocated)
	}
	bytesPerSample := int(bitsAllocated / 8)

	// Sample interpretation (5400,1006)
	sampleInterp, _ := getStringFromSeq(waveform, tag.Tag{Group: 0x5400, Element: 0x1006})
	sampleInterp = strings.TrimSpace(sampleInterp)
	isSigned := sampleInterp == "SS" || sampleInterp == "SB" || sampleInterp == ""

	// Channel Definition Sequence (003A,0200)
	chanDefs, err := getSeqItems(waveform, tag.Tag{Group: 0x003A, Element: 0x0200})
	if err != nil {
		return nil, fmt.Errorf("ecgwaveform: channel definition sequence not found")
	}

	type chanMeta struct {
		label           string
		sensitivity     float64
		sensitivityUnit string
		correction      float64
		baseline        float64
	}

	metas := make([]chanMeta, len(chanDefs))
	for i, cd := range chanDefs {
		label := fmt.Sprintf("Ch%d", i+1)

		// Channel Source Sequence (003A,0208) → Code Meaning (0008,0104)
		if srcSeqs, err := getSeqItems(cd, tag.Tag{Group: 0x003A, Element: 0x0208}); err == nil && len(srcSeqs) > 0 {
			if meaning, err := getStringFromSeq(srcSeqs[0], tag.Tag{Group: 0x0008, Element: 0x0104}); err == nil {
				label = normalizeLeadName(meaning)
			}
		}

		sensitivity := 1.0
		if s, err := getFloat64FromSeq(cd, tag.Tag{Group: 0x003A, Element: 0x0210}); err == nil {
			sensitivity = s
		}

		sensitivityUnit := "mV"
		if unitSeqs, err := getSeqItems(cd, tag.Tag{Group: 0x003A, Element: 0x0211}); err == nil && len(unitSeqs) > 0 {
			if code, err := getStringFromSeq(unitSeqs[0], tag.Tag{Group: 0x0008, Element: 0x0100}); err == nil {
				sensitivityUnit = strings.TrimSpace(code)
			}
		}

		correction := 1.0
		if c, err := getFloat64FromSeq(cd, tag.Tag{Group: 0x003A, Element: 0x0212}); err == nil {
			correction = c
		}

		baseline := 0.0
		if b, err := getFloat64FromSeq(cd, tag.Tag{Group: 0x003A, Element: 0x0213}); err == nil {
			baseline = b
		}

		metas[i] = chanMeta{label: label, sensitivity: sensitivity, sensitivityUnit: sensitivityUnit, correction: correction, baseline: baseline}
	}

	// Waveform Data (5400,1010)
	waveformDataElem, err := findInSeqItem(waveform, tag.Tag{Group: 0x5400, Element: 0x1010})
	if err != nil {
		return nil, fmt.Errorf("ecgwaveform: waveform data not found")
	}
	rawData, ok := waveformDataElem.Value.GetValue().([]byte)
	if !ok {
		return nil, fmt.Errorf("ecgwaveform: waveform data is not bytes")
	}

	n := int(numSamples)
	ch := int(numChannels)

	rawChannels := make([][]float32, ch)
	for i := range rawChannels {
		rawChannels[i] = make([]float32, n)
	}

	for s := 0; s < n; s++ {
		for c := 0; c < ch; c++ {
			offset := (s*ch + c) * bytesPerSample
			if offset+bytesPerSample > len(rawData) {
				break
			}
			var raw float64
			if bytesPerSample == 2 {
				v := binary.LittleEndian.Uint16(rawData[offset:])
				if isSigned {
					raw = float64(int16(v))
				} else {
					raw = float64(v)
				}
			} else {
				b := rawData[offset]
				if isSigned {
					raw = float64(int8(b))
				} else {
					raw = float64(b)
				}
			}
			rawChannels[c][s] = float32(raw)
		}
	}

	rawLeadOrder := make([]string, ch)
	byLabel := make(map[string][]float32)
	for i, meta := range metas {
		if i >= ch {
			break
		}
		rawLeadOrder[i] = meta.label
		uv := strings.ToLower(meta.sensitivityUnit)
		unitToMv := 1.0
		if strings.Contains(uv, "uv") || uv == "µv" {
			unitToMv = 0.001
		}
		factor := meta.sensitivity * meta.correction * unitToMv
		baselineMv := meta.baseline * unitToMv
		samples := make([]float32, n)
		for s := 0; s < n; s++ {
			samples[s] = float32(float64(rawChannels[i][s])*factor + baselineMv)
		}
		byLabel[meta.label] = samples
	}

	// Build 12 standard leads with derivation if needed
	finalChannels := make([]EcgChannel, 12)
	for i, leadName := range standardLeads12 {
		if samples, ok := byLabel[leadName]; ok {
			finalChannels[i] = EcgChannel{Label: leadName, Samples: samples}
		} else {
			derived, err := deriveLead(leadName, byLabel, n)
			if err != nil {
				return nil, err
			}
			finalChannels[i] = EcgChannel{Label: leadName, Samples: derived}
		}
	}

	return &EcgRecord{
		Channels:          finalChannels,
		SamplingFrequency: samplingFreq,
		DurationSec:       float64(n) / samplingFreq,
		PatientName:       patientName,
		AcquisitionDate:   acquisitionDate,
		RawLeadOrder:      rawLeadOrder,
	}, nil
}

// deriveLead computes missing limb leads from I and II.
func deriveLead(name string, byLabel map[string][]float32, n int) ([]float32, error) {
	I, okI := byLabel["I"]
	II, okII := byLabel["II"]
	if !okI || !okII {
		return nil, fmt.Errorf("ecgwaveform: cannot derive %s: leads I and II required", name)
	}
	s := make([]float32, n)
	switch name {
	case "III":
		for i := range s {
			s[i] = II[i] - I[i]
		}
	case "aVR":
		for i := range s {
			s[i] = -(I[i] + II[i]) * 0.5
		}
	case "aVL":
		for i := range s {
			s[i] = I[i] - II[i]*0.5
		}
	case "aVF":
		for i := range s {
			s[i] = II[i] - I[i]*0.5
		}
	default:
		return nil, fmt.Errorf("ecgwaveform: lead %s not found and not derivable", name)
	}
	return s, nil
}

// EncodeWire encodes an EcgRecord into the binary wire format:
// [uint32 LE jsonLength][JSON metadata padded to 4][Float32 LE samples channel-major]
func EncodeWire(rec *EcgRecord, displayDurationSec float64) ([]byte, error) {
	// Truncate to display duration
	n := len(rec.Channels[0].Samples)
	if displayDurationSec > 0 && displayDurationSec < rec.DurationSec {
		n = int(math.Round(displayDurationSec * rec.SamplingFrequency))
		if n > len(rec.Channels[0].Samples) {
			n = len(rec.Channels[0].Samples)
		}
	}

	labels := make([]string, len(rec.Channels))
	for i, ch := range rec.Channels {
		labels[i] = ch.Label
	}

	type meta struct {
		NumChannels       int      `json:"numChannels"`
		NumSamples        int      `json:"numSamples"`
		SamplingFrequency float64  `json:"samplingFrequency"`
		DurationSec       float64  `json:"durationSec"`
		PatientName       string   `json:"patientName,omitempty"`
		AcquisitionDate   string   `json:"acquisitionDate,omitempty"`
		RawLeadOrder      []string `json:"rawLeadOrder,omitempty"`
		ChannelLabels     []string `json:"channelLabels"`
	}

	m := meta{
		NumChannels:       len(rec.Channels),
		NumSamples:        n,
		SamplingFrequency: rec.SamplingFrequency,
		DurationSec:       float64(n) / rec.SamplingFrequency,
		PatientName:       rec.PatientName,
		AcquisitionDate:   rec.AcquisitionDate,
		RawLeadOrder:      rec.RawLeadOrder,
		ChannelLabels:     labels,
	}

	jsonBytes, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}

	// Pad JSON to multiple of 4
	padded := len(jsonBytes)
	if padded%4 != 0 {
		padded += 4 - padded%4
	}
	jsonPadded := make([]byte, padded)
	copy(jsonPadded, jsonBytes)
	for i := len(jsonBytes); i < padded; i++ {
		jsonPadded[i] = ' '
	}

	var buf bytes.Buffer
	// uint32 LE: json length
	var jsonLen [4]byte
	binary.LittleEndian.PutUint32(jsonLen[:], uint32(padded))
	buf.Write(jsonLen[:])
	buf.Write(jsonPadded)

	// Float32 LE samples, channel-major
	sampleBuf := make([]byte, 4)
	for _, ch := range rec.Channels {
		for s := 0; s < n; s++ {
			binary.LittleEndian.PutUint32(sampleBuf, math.Float32bits(ch.Samples[s]))
			buf.Write(sampleBuf)
		}
	}

	return buf.Bytes(), nil
}

// ─── DICOM helpers ────────────────────────────────────────────────────────────

// findInSeqItem searches for a tag within a SequenceItemValue's elements.
func findInSeqItem(item *dicom.SequenceItemValue, t tag.Tag) (*dicom.Element, error) {
	elems, ok := item.GetValue().([]*dicom.Element)
	if !ok {
		return nil, fmt.Errorf("tag %v: invalid sequence item", t)
	}
	for _, e := range elems {
		if e.Tag == t {
			return e, nil
		}
	}
	return nil, fmt.Errorf("tag %v not found", t)
}

func getUint16FromSeq(item *dicom.SequenceItemValue, t tag.Tag) (uint16, error) {
	elem, err := findInSeqItem(item, t)
	if err != nil {
		return 0, err
	}
	vals, ok := elem.Value.GetValue().([]int)
	if !ok || len(vals) == 0 {
		return 0, fmt.Errorf("tag %v: not int slice", t)
	}
	return uint16(vals[0]), nil
}

func getUint32FromSeq(item *dicom.SequenceItemValue, t tag.Tag) (uint32, error) {
	elem, err := findInSeqItem(item, t)
	if err != nil {
		return 0, err
	}
	vals, ok := elem.Value.GetValue().([]int)
	if !ok || len(vals) == 0 {
		return 0, fmt.Errorf("tag %v: not int slice", t)
	}
	return uint32(vals[0]), nil
}

func getStringFromSeq(item *dicom.SequenceItemValue, t tag.Tag) (string, error) {
	elem, err := findInSeqItem(item, t)
	if err != nil {
		return "", err
	}
	vals, ok := elem.Value.GetValue().([]string)
	if !ok || len(vals) == 0 {
		return "", fmt.Errorf("tag %v: not string slice", t)
	}
	return vals[0], nil
}

func getFloat64FromSeq(item *dicom.SequenceItemValue, t tag.Tag) (float64, error) {
	s, err := getStringFromSeq(item, t)
	if err != nil {
		return 0, err
	}
	var f float64
	_, scanErr := fmt.Sscanf(strings.TrimSpace(s), "%f", &f)
	return f, scanErr
}

func getSeqItems(item *dicom.SequenceItemValue, t tag.Tag) ([]*dicom.SequenceItemValue, error) {
	elem, err := findInSeqItem(item, t)
	if err != nil {
		return nil, err
	}
	seqs, ok := elem.Value.GetValue().([]*dicom.SequenceItemValue)
	if !ok {
		return nil, fmt.Errorf("tag %v: not sequence", t)
	}
	return seqs, nil
}

func normalizeLeadName(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "lead ")
	s = strings.ReplaceAll(s, " ", "")
	m := map[string]string{
		"i": "I", "ii": "II", "iii": "III",
		"avr": "aVR", "avl": "aVL", "avf": "aVF",
		"v1": "V1", "v2": "V2", "v3": "V3",
		"v4": "V4", "v5": "V5", "v6": "V6",
	}
	if v, ok := m[s]; ok {
		return v
	}
	return strings.TrimSpace(raw)
}
