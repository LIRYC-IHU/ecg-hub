package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

type ECGMeta struct {
	PatientID       string
	RecordedAt      time.Time
	DeviceModel     string
	DurationSeconds float64
	SampleRate      float64
	Extra           map[string]any
}

type nkMetadataJSON struct {
	PatientID    string  `json:"patientID"`
	FamilyName   string  `json:"familyName"`
	GivenName    string  `json:"givenName"`
	Gender       string  `json:"gender"`
	BirthDate    string  `json:"birthDate"`
	Location     string  `json:"location"`
	DeviceModel  string  `json:"deviceModel"`
	Datetime     string  `json:"datetime"`
	HeartRate    int     `json:"heartRate"`
	PRInterval   int     `json:"prInterval"`
	QRSDuration  int     `json:"qrsDuration"`
	QTInterval   int     `json:"qtInterval"`
	QTcInterval  int     `json:"qtcInterval"`
	PAxis        int     `json:"pAxis"`
	QRSAxis      int     `json:"qrsAxis"`
	TAxis        int     `json:"tAxis"`
	V5RAmplitude float64 `json:"v5rAmplitude"`
	V1SAmplitude float64 `json:"v1sAmplitude"`
	SampleRate   int     `json:"sampleRate"`
	TotalSamples int     `json:"totalSamples"`
}

func nkBinary() string {
	if v := os.Getenv("BRIDGE_NK_TO_FDA"); v != "" {
		return v
	}
	if dir := os.Getenv("BRIDGE_BIN_DIR"); dir != "" {
		return dir + "/nk-to-fda"
	}
	return "nk-to-fda"
}

func parse(ctx context.Context, data []byte) (*ECGMeta, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("nihon-kohden: empty data")
	}

	tmp, err := os.CreateTemp("", "nk-parse-*.DAT")
	if err != nil {
		return nil, fmt.Errorf("nihon-kohden: create temp: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("nihon-kohden: write temp: %w", err)
	}
	tmp.Close()

	cmd := exec.CommandContext(ctx, nkBinary(), "--input", tmp.Name(), "--metadata-json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("nihon-kohden: nk-to-fda: %s: %w", stderr.String(), err)
	}

	var md nkMetadataJSON
	if err := json.Unmarshal(stdout.Bytes(), &md); err != nil {
		return nil, fmt.Errorf("nihon-kohden: json decode: %w", err)
	}

	var recordedAt time.Time
	if md.Datetime != "" {
		if t, err := time.Parse("20060102150405", md.Datetime); err == nil {
			recordedAt = t
		}
	}

	var durationSeconds float64
	if md.SampleRate > 0 && md.TotalSamples > 0 {
		durationSeconds = float64(md.TotalSamples) / float64(md.SampleRate)
	}

	extra := map[string]any{}
	if md.FamilyName != "" {
		extra["last_name"] = md.FamilyName
	}
	if md.GivenName != "" {
		extra["first_name"] = md.GivenName
	}
	if md.Gender != "" {
		extra["sex"] = md.Gender
	}
	if md.BirthDate != "" {
		extra["birth_date"] = md.BirthDate
	}
	if md.Location != "" {
		extra["location"] = md.Location
	}
	if md.HeartRate > 0 {
		extra["heart_rate"] = md.HeartRate
	}
	if md.PRInterval > 0 {
		extra["pr_interval"] = md.PRInterval
	}
	if md.QRSDuration > 0 {
		extra["qrs_duration"] = md.QRSDuration
	}
	if md.QTInterval > 0 {
		extra["qt_interval"] = md.QTInterval
	}
	if md.QTcInterval > 0 {
		extra["qtc_interval"] = md.QTcInterval
	}

	return &ECGMeta{
		PatientID:       md.PatientID,
		RecordedAt:      recordedAt,
		DeviceModel:     md.DeviceModel,
		DurationSeconds: durationSeconds,
		SampleRate:      float64(md.SampleRate),
		Extra:           extra,
	}, nil
}
