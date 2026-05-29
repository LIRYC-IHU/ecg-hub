package main

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	dicomlib "github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"
)

type ECGMeta struct {
	PatientID  string
	RecordedAt time.Time
	Extra      map[string]any
}

func validate(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("dicom: empty data")
	}
	if len(data) >= 132 && string(data[128:132]) == "DICM" {
		return nil
	}
	return fmt.Errorf("dicom: DICM preamble not found at bytes 128-131")
}

func parse(data []byte) (*ECGMeta, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("dicom: empty data")
	}

	dataset, err := dicomlib.ParseUntilEOF(bytes.NewReader(data), nil)
	if err != nil {
		return nil, fmt.Errorf("dicom: parse: %w", err)
	}

	patientID, err := extractString(dataset, tag.PatientID)
	if err != nil || strings.TrimSpace(patientID) == "" {
		return nil, fmt.Errorf("dicom: missing PatientID (0010,0020)")
	}

	recordedAt := parseStudyDateTime(dataset)

	extra := map[string]any{}
	if sopUID, err := extractString(dataset, tag.SOPInstanceUID); err == nil && sopUID != "" {
		extra["sop_instance_uid"] = sopUID
	}
	if modality, err := extractString(dataset, tag.Modality); err == nil && modality != "" {
		extra["modality"] = modality
	}

	return &ECGMeta{
		PatientID:  strings.TrimSpace(patientID),
		RecordedAt: recordedAt,
		Extra:      extra,
	}, nil
}

func rewritePatientID(data []byte, newID string) ([]byte, error) {
	dataset, err := dicomlib.ParseUntilEOF(bytes.NewReader(data), nil)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	newElem, err := dicomlib.NewElement(tag.PatientID, []string{newID})
	if err != nil {
		return nil, fmt.Errorf("new element: %w", err)
	}

	replaced := false
	for i, elem := range dataset.Elements {
		if elem.Tag == tag.PatientID {
			dataset.Elements[i] = newElem
			replaced = true
			break
		}
	}
	if !replaced {
		dataset.Elements = append(dataset.Elements, newElem)
	}

	var buf bytes.Buffer
	if err := dicomlib.Write(&buf, dataset); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	return buf.Bytes(), nil
}

func extractString(ds dicomlib.Dataset, t tag.Tag) (string, error) {
	elem, err := ds.FindElementByTag(t)
	if err != nil {
		return "", err
	}
	vals, ok := elem.Value.GetValue().([]string)
	if !ok || len(vals) == 0 {
		return "", fmt.Errorf("tag %v is not a string", t)
	}
	return vals[0], nil
}

func parseStudyDateTime(ds dicomlib.Dataset) time.Time {
	dateStr, err := extractString(ds, tag.StudyDate)
	if err != nil || strings.TrimSpace(dateStr) == "" {
		return time.Time{}
	}
	dateStr = strings.TrimSpace(dateStr)

	timeStr, _ := extractString(ds, tag.StudyTime)
	timeStr = strings.TrimSpace(timeStr)

	if len(timeStr) >= 6 {
		combined := dateStr + timeStr[:6]
		if t, err := time.ParseInLocation("20060102150405", combined, time.UTC); err == nil {
			return t
		}
	}

	if t, err := time.ParseInLocation("20060102", dateStr, time.UTC); err == nil {
		return t
	}
	return time.Time{}
}
