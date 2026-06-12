package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/ecgwaveform"
	"github.com/LIRYC-IHU/ecg-hub/internal/export"
	"github.com/LIRYC-IHU/ecg-hub/internal/storage"
)

const waveformDisplayDuration = 10.0 // seconds shown by default

// ECGWaveformHandler handles GET /api/v1/ecgs/:id/waveform.
// For DICOM files: parses directly.
// For other formats: converts to DICOM via the bridge first, then parses.
// Returns the binary wire format expected by the ECGViewer React component.
func ECGWaveformHandler(db *gorm.DB, volumePath string, bridge export.Converter) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		ecgRepo := repository.NewECGRepository(db)
		ecg, err := ecgRepo.FindByID(id)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{
				"code":    "ECG_NOT_FOUND",
				"message": "ECG not found",
			})
		}

		// Resolve file path
		filePath := ecg.FilePath
		if !filepath.IsAbs(filePath) {
			filePath = filepath.Join(volumePath, filePath)
		}

		// Integrity check before serving waveform data.
		if err := storage.VerifyFile(filePath, ecg.ContentHash); err != nil {
			return c.JSON(http.StatusUnprocessableEntity, map[string]string{
				"code":    "INTEGRITY_FAILURE",
				"message": "ECG file integrity check failed — the file may have been modified",
			})
		}

		var dicomData []byte

		if ecg.Vendor == "dicom" {
			// Already DICOM — read directly
			dicomData, err = os.ReadFile(filePath)
			if err != nil {
				return c.JSON(http.StatusNotFound, map[string]string{
					"code":    "FILE_NOT_FOUND",
					"message": "ECG file not found on disk",
				})
			}
		} else {
			// Convert to DICOM via bridge
			if bridge == nil || !bridge.SupportsFormat(ecg.Vendor, "dicom") {
				return c.JSON(http.StatusUnprocessableEntity, map[string]string{
					"code":    "NO_CONVERTER",
					"message": "ECG viewer requires DICOM format — no converter available for vendor: " + ecg.Vendor,
				})
			}

			ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
			defer cancel()

			// Fetch patient for enriching DICOM metadata (optional)
			var patient *models.Patient
			patRepo := repository.NewPatientRepository(db)
			if p, err := patRepo.FindByPatientID(ecg.PatientID); err == nil {
				patient = p
			}

			dicomData, err = bridge.Convert(ctx, filePath, ecg.Vendor, "dicom", patient, export.ConvertOptions{})
			if err != nil {
				// Log the underlying error server-side; return a generic message so
				// internal paths / converter details are not exposed to the client.
				slog.Error("ecg waveform: conversion failed", "ecg_id", id, "vendor", ecg.Vendor, "error", err)
				return c.JSON(http.StatusUnprocessableEntity, map[string]string{
					"code":    "CONVERSION_ERROR",
					"message": "Failed to convert ECG to DICOM",
				})
			}
		}

		record, err := ecgwaveform.Parse(dicomData)
		if err != nil {
			slog.Error("ecg waveform: parse failed", "ecg_id", id, "vendor", ecg.Vendor, "error", err)
			return c.JSON(http.StatusUnprocessableEntity, map[string]string{
				"code":    "PARSE_ERROR",
				"message": "Failed to parse ECG waveform",
			})
		}

		wire, err := ecgwaveform.EncodeWire(record, waveformDisplayDuration)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "ENCODE_ERROR",
				"message": "Failed to encode waveform",
			})
		}

		c.Response().Header().Set("Content-Type", "application/octet-stream")
		c.Response().Header().Set("Content-Disposition", "inline")
		return c.Blob(http.StatusOK, "application/octet-stream", wire)
	}
}
