package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"
)

// ECGMeta is the internal metadata representation returned by parse().
type ECGMeta struct {
	PatientID       string
	RecordedAt      time.Time
	SourceFormat    string
	DeviceModel     string
	LeadCount       int
	DurationSeconds float64
	SampleRate      float64
	Extra           map[string]any
}

func validate(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("philips: empty data")
	}
	var doc philipsDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("philips: not a valid Philips XML: %w", err)
	}
	if doc.Patient.General.PatientID == "" {
		return fmt.Errorf("philips: missing patientid")
	}
	return nil
}

func parse(data []byte) (*ECGMeta, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("philips: empty data")
	}

	var doc philipsDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("philips: xml unmarshal: %w", err)
	}

	patientID := doc.Patient.General.PatientID
	if patientID == "" {
		return nil, fmt.Errorf("philips: missing patientid")
	}

	recordedAt, _ := parseDateTime(doc.DataAcq.Date, doc.DataAcq.Time)

	nrow := doc.ReportInfo.ReportFormat.WaveformFormat.Main.NRow
	ncol := doc.ReportInfo.ReportFormat.WaveformFormat.Main.NColumn
	leadCount := nrow * ncol

	var durationSeconds float64
	if durationMs := doc.Waveforms.Parsed.DurationPerChannel; durationMs > 0 {
		durationSeconds = float64(durationMs) / 1000.0
	}

	var sampleRate float64
	if sr := doc.DataAcq.SignalChars.SamplingRate; sr > 0 {
		sampleRate = float64(sr)
	}

	extra := map[string]any{
		"document_type":    doc.DocInfo.DocType,
		"document_version": doc.DocInfo.DocVersion,
	}
	if doc.Patient.General.Name.LastName != "" {
		extra["last_name"] = doc.Patient.General.Name.LastName
	}
	if doc.Patient.General.Name.FirstName != "" {
		extra["first_name"] = doc.Patient.General.Name.FirstName
	}
	if doc.Patient.General.Sex != "" {
		extra["sex"] = doc.Patient.General.Sex
	}

	return &ECGMeta{
		PatientID:       patientID,
		RecordedAt:      recordedAt,
		SourceFormat:    "philips_sierraecg_xml",
		DeviceModel:     doc.DataAcq.Machine.Detail,
		LeadCount:       leadCount,
		DurationSeconds: durationSeconds,
		SampleRate:      sampleRate,
		Extra:           extra,
	}, nil
}

func parseDateTime(date, t string) (time.Time, error) {
	if date == "" {
		return time.Time{}, fmt.Errorf("empty acquisition date")
	}
	layout := "2006-01-02 15:04:05"
	parsed, err := time.ParseInLocation(layout, date+" "+t, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse datetime %q %q: %w", date, t, err)
	}
	return parsed, nil
}

// --- XML document structure ---

type philipsDoc struct {
	XMLName xml.Name `xml:"http://www3.medical.philips.com restingecgdata"`

	DocInfo struct {
		DocType    string `xml:"documenttype"`
		DocVersion string `xml:"documentversion"`
	} `xml:"documentinfo"`

	DataAcq struct {
		Date string `xml:"date,attr"`
		Time string `xml:"time,attr"`

		Machine struct {
			Detail string `xml:"detaildescription,attr"`
		} `xml:"machine"`

		SignalChars struct {
			SamplingRate int `xml:"samplingrate"`
		} `xml:"signalcharacteristics"`
	} `xml:"dataacquisition"`

	ReportInfo struct {
		ReportFormat struct {
			WaveformFormat struct {
				Main struct {
					NRow    int `xml:"nrow,attr"`
					NColumn int `xml:"ncolumn,attr"`
				} `xml:"mainwaveformformat"`
			} `xml:"waveformformat"`
		} `xml:"reportformat"`
	} `xml:"reportinfo"`

	Patient struct {
		General struct {
			PatientID string `xml:"patientid"`
			Name      struct {
				LastName  string `xml:"lastname"`
				FirstName string `xml:"firstname"`
			} `xml:"name"`
			Sex string `xml:"sex"`
		} `xml:"generalpatientdata"`
	} `xml:"patient"`

	Waveforms struct {
		Parsed struct {
			DurationPerChannel int `xml:"durationperchannel,attr"`
		} `xml:"parsedwaveforms"`
	} `xml:"waveforms"`
}

// --- XML streaming update logic ---

type fieldPathEntry struct {
	path []string
	attr string
}

var fieldPaths = map[string]fieldPathEntry{
	"patient_id":       {path: []string{"restingecgdata", "patient", "generalpatientdata", "patientid"}},
	"last_name":        {path: []string{"restingecgdata", "patient", "generalpatientdata", "name", "lastname"}},
	"first_name":       {path: []string{"restingecgdata", "patient", "generalpatientdata", "name", "firstname"}},
	"sex":              {path: []string{"restingecgdata", "patient", "generalpatientdata", "sex"}},
	"document_type":    {path: []string{"restingecgdata", "documentinfo", "documenttype"}},
	"document_version": {path: []string{"restingecgdata", "documentinfo", "documentversion"}},
	"sample_rate":      {path: []string{"restingecgdata", "dataacquisition", "signalcharacteristics", "samplingrate"}},
}

var dataAcqPath = []string{"restingecgdata", "dataacquisition"}
var machinePath = []string{"restingecgdata", "dataacquisition", "machine"}

func applyUpdates(data []byte, fields map[string]string) ([]byte, error) {
	contentUpdates := map[string]string{}
	for fieldKey, val := range fields {
		if val == "" {
			continue
		}
		if entry, ok := fieldPaths[fieldKey]; ok && entry.attr == "" {
			contentUpdates[pathKey(entry.path)] = val
		}
	}

	var newDate, newTime string
	if ra, ok := fields["recorded_at"]; ok && ra != "" {
		t, err := time.Parse(time.RFC3339, ra)
		if err == nil {
			newDate = t.UTC().Format("2006-01-02")
			newTime = t.UTC().Format("15:04:05")
		}
	}
	newDeviceModel := fields["device_model"]

	dec := xml.NewDecoder(bytes.NewReader(data))
	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)

	var stack []string
	var replaceContent string
	replaceDepth := -1

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode token: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			depth := len(stack)

			if pathEqual(stack, dataAcqPath) && (newDate != "" || newTime != "") {
				t.Attr = updateAttr(t.Attr, "date", newDate)
				t.Attr = updateAttr(t.Attr, "time", newTime)
			}
			if pathEqual(stack, machinePath) && newDeviceModel != "" {
				t.Attr = updateAttr(t.Attr, "detaildescription", newDeviceModel)
			}

			if err := enc.EncodeToken(t); err != nil {
				return nil, err
			}

			if newVal, ok := contentUpdates[pathKey(stack)]; ok {
				replaceContent = newVal
				replaceDepth = depth
			}

		case xml.EndElement:
			if replaceDepth == len(stack) && replaceContent != "" {
				if err := enc.EncodeToken(xml.CharData(replaceContent)); err != nil {
					return nil, err
				}
				replaceContent = ""
				replaceDepth = -1
			}
			stack = stack[:len(stack)-1]
			if err := enc.EncodeToken(t); err != nil {
				return nil, err
			}

		case xml.CharData:
			if replaceDepth == len(stack) && replaceContent != "" {
				if err := enc.EncodeToken(xml.CharData(replaceContent)); err != nil {
					return nil, err
				}
				replaceContent = ""
				replaceDepth = -1
			} else {
				if err := enc.EncodeToken(t); err != nil {
					return nil, err
				}
			}

		default:
			if err := enc.EncodeToken(tok); err != nil {
				return nil, err
			}
		}
	}

	if err := enc.Flush(); err != nil {
		return nil, fmt.Errorf("encode flush: %w", err)
	}
	return buf.Bytes(), nil
}

func pathKey(path []string) string { return strings.Join(path, "/") }

func pathEqual(stack, path []string) bool {
	if len(stack) != len(path) {
		return false
	}
	for i, p := range path {
		if stack[i] != p {
			return false
		}
	}
	return true
}

func updateAttr(attrs []xml.Attr, key, val string) []xml.Attr {
	if val == "" {
		return attrs
	}
	for i, a := range attrs {
		if a.Name.Local == key {
			attrs[i].Value = val
			return attrs
		}
	}
	return append(attrs, xml.Attr{Name: xml.Name{Local: key}, Value: val})
}
