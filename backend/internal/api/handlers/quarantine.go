package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
	"github.com/LIRYC-IHU/ecg-hub/internal/module"
)

// QuarantineEntryDTO is the JSON representation of a quarantine entry.
type QuarantineEntryDTO struct {
	ID          string         `json:"id"`
	Filename    string         `json:"filename"`
	FilePath    string         `json:"file_path"`
	ReceivedAt  string         `json:"received_at"` // RFC3339
	ErrorReason string         `json:"error_reason"`
	Category    string         `json:"category"` // "error" | "unidentified"
	Vendor      string         `json:"vendor,omitempty"`
	RecordedAt  string         `json:"recorded_at,omitempty"` // RFC3339, unidentified only
	Metadata    map[string]any `json:"metadata,omitempty"`    // extracted demographics, unidentified only
}

func quarantineToDTO(e models.QuarantineEntry) QuarantineEntryDTO {
	dto := QuarantineEntryDTO{
		ID:          e.ID,
		Filename:    e.Filename,
		FilePath:    e.FilePath,
		ReceivedAt:  e.ReceivedAt.UTC().Format(time.RFC3339),
		ErrorReason: e.ErrorReason,
		Category:    e.Category,
		Vendor:      e.Vendor,
	}
	if dto.Category == "" {
		dto.Category = models.QuarantineCategoryError
	}
	if e.RecordedAt != nil {
		dto.RecordedAt = e.RecordedAt.UTC().Format(time.RFC3339)
	}
	if len(e.Metadata) > 0 {
		var m map[string]any
		if err := json.Unmarshal(e.Metadata, &m); err == nil {
			dto.Metadata = m
		}
	}
	return dto
}

// ListQuarantineHandler handles GET /api/v1/admin/quarantine.
// Returns paginated list of quarantine entries, ordered newest-first.
//
//	@Summary		List quarantined files
//	@Description	Returns a paginated list of quarantine entries, ordered newest-first.
//	@Tags			Quarantine
//	@Produce		json
//	@Param			page		query	int	false	"Page number"
//	@Param			per_page	query	int	false	"Items per page"
//	@Success		200	{object}	map[string]interface{}
//	@Security		BearerAuth
//	@Router			/api/v1/admin/quarantine [get]
func ListQuarantineHandler(db *gorm.DB) echo.HandlerFunc {
	repo := repository.NewQuarantineRepository(db)
	return func(c echo.Context) error {
		page, _ := strconv.Atoi(c.QueryParam("page"))
		perPage, _ := strconv.Atoi(c.QueryParam("per_page"))
		if page < 1 {
			page = 1
		}
		if perPage < 1 {
			perPage = 50
		}
		category := c.QueryParam("category")

		entries, total, err := repo.List(page, perPage, category)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}

		dtos := make([]QuarantineEntryDTO, len(entries))
		for i, e := range entries {
			dtos[i] = quarantineToDTO(e)
		}

		return c.JSON(http.StatusOK, map[string]any{
			"data":     dtos,
			"total":    total,
			"page":     page,
			"per_page": perPage,
		})
	}
}

// DeleteQuarantineHandler handles DELETE /api/v1/admin/quarantine/:id.
// Deletes the DB record and removes the physical file from the quarantine directory.
//
//	@Summary		Delete quarantine entry
//	@Description	Deletes the quarantine DB record and removes the physical file from the quarantine directory.
//	@Tags			Quarantine
//	@Param			id	path	string	true	"Quarantine entry UUID"
//	@Success		204
//	@Failure		404	{object}	map[string]string
//	@Security		BearerAuth
//	@Router			/api/v1/admin/quarantine/{id} [delete]
func DeleteQuarantineHandler(db *gorm.DB) echo.HandlerFunc {
	repo := repository.NewQuarantineRepository(db)
	return func(c echo.Context) error {
		id := c.Param("id")

		filePath, err := repo.DeleteByID(id)
		if err != nil {
			if errors.Is(err, repository.ErrQuarantineNotFound) {
				return c.JSON(http.StatusNotFound, mw.APIError("NOT_FOUND", "quarantine entry not found"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "delete failed"))
		}

		// Best-effort physical file removal.
		if filePath != "" {
			if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
				slog.Warn("quarantine: file removal failed", "path", filePath, "error", err)
			}
		}

		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, userID, "quarantine_decision",
			id, map[string]any{
				"action": "delete",
				"id":     id,
				"file":   filePath,
			})

		return c.NoContent(http.StatusNoContent)
	}
}

// assignRequest is the body of POST /api/v1/admin/quarantine/:id/assign.
// CreateNew must be set explicitly to assign to a patient_id that does not yet
// exist in ECG Hub (a new patient created on re-ingest, then enriched via HL7).
// Without it, an unknown patient_id is rejected — guarding against typos.
type assignRequest struct {
	PatientID string `json:"patient_id"`
	CreateNew bool   `json:"create_new"`
}

// reingester is the subset of *ingestion.Persister used to re-ingest an assigned file.
type reingester interface {
	PersistRouted(ri ingestion.RoutedItem) error
}

// AssignQuarantineHandler handles POST /api/v1/admin/quarantine/:id/assign.
// It assigns a patient ID to an "unidentified" quarantine entry, re-ingests the
// raw file through the normal persistence pipeline (which upserts the patient,
// inserts the ECG and fires HL7 enrichment), then removes the quarantine entry.
//
//	@Summary		Assign a patient to an unidentified ECG
//	@Description	Assigns a patient ID to an unidentified quarantine entry and re-ingests the file through the normal pipeline (patient upsert + ECG insert + HL7 enrichment).
//	@Tags			Quarantine
//	@Accept			json
//	@Produce		json
//	@Param			id		path	string			true	"Quarantine entry UUID"
//	@Param			body	body	assignRequest	true	"Patient ID to assign"
//	@Success		200	{object}	map[string]interface{}
//	@Failure		400	{object}	map[string]string
//	@Failure		404	{object}	map[string]string
//	@Failure		422	{object}	map[string]string
//	@Security		BearerAuth
//	@Router			/api/v1/admin/quarantine/{id}/assign [post]
func AssignQuarantineHandler(persister reingester, db *gorm.DB) echo.HandlerFunc {
	repo := repository.NewQuarantineRepository(db)
	return func(c echo.Context) error {
		id := c.Param("id")

		var req assignRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "invalid request body"))
		}
		req.PatientID = strings.TrimSpace(req.PatientID)
		if req.PatientID == "" {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "patient_id is required"))
		}

		// Anti wrong-patient guard. Two legitimate cases:
		//   1. Patient already exists → assign (UI confirmed name + DOB).
		//   2. Patient unknown → only allowed when the caller explicitly opts in via
		//      create_new (a new patient ID from the HIS, created on re-ingest and
		//      enriched via HL7). Default path rejects unknown IDs to catch typos.
		var patient models.Patient
		patientExists := true
		if err := db.Where("patient_id = ?", req.PatientID).First(&patient).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "patient lookup failed"))
			}
			patientExists = false
			if !req.CreateNew {
				return c.JSON(http.StatusNotFound, mw.APIError("PATIENT_NOT_FOUND", "patient inconnu — vérifiez l'identifiant ou créez un nouveau patient"))
			}
		}

		entry, err := repo.FindByID(id)
		if err != nil {
			if errors.Is(err, repository.ErrQuarantineNotFound) {
				return c.JSON(http.StatusNotFound, mw.APIError("NOT_FOUND", "quarantine entry not found"))
			}
			return c.JSON(http.StatusInternalServerError, mw.APIError("DB_ERROR", "query failed"))
		}

		if entry.Category != models.QuarantineCategoryUnidentified {
			return c.JSON(http.StatusBadRequest, mw.APIError("NOT_UNIDENTIFIED", "only unidentified entries can be assigned to a patient"))
		}
		if entry.FilePath == "" {
			return c.JSON(http.StatusUnprocessableEntity, mw.APIError("NO_RAW_FILE", "raw file unavailable; cannot re-ingest"))
		}

		data, err := os.ReadFile(entry.FilePath)
		if err != nil {
			slog.Error("quarantine: assign read file failed", "path", entry.FilePath, "error", err)
			return c.JSON(http.StatusUnprocessableEntity, mw.APIError("NO_RAW_FILE", "raw file unreadable; cannot re-ingest"))
		}

		// Rebuild the parsed metadata and stamp the assigned patient ID.
		meta := &module.ECGMetadata{}
		if len(entry.Metadata) > 0 {
			if err := json.Unmarshal(entry.Metadata, meta); err != nil {
				slog.Warn("quarantine: assign metadata decode failed", "id", id, "error", err)
			}
		}
		meta.PatientID = req.PatientID
		if meta.VendorName == "" {
			meta.VendorName = entry.Vendor
		}
		if meta.RecordedAt.IsZero() && entry.RecordedAt != nil {
			meta.RecordedAt = *entry.RecordedAt
		}

		ri := ingestion.RoutedItem{
			IngestItem: ingestion.IngestItem{
				Filename: entry.Filename,
				Data:     data,
				Source:   "manual_assign",
			},
			Meta:       meta,
			ModuleName: meta.VendorName,
		}
		if err := persister.PersistRouted(ri); err != nil {
			slog.Error("quarantine: assign re-ingest failed", "id", id, "patient_id", req.PatientID, "error", err)
			return c.JSON(http.StatusInternalServerError, mw.APIError("REINGEST_FAILED", "failed to re-ingest the assigned ECG"))
		}

		// Re-ingestion succeeded — remove the quarantine entry and its raw file.
		filePath, delErr := repo.DeleteByID(id)
		if delErr != nil {
			slog.Warn("quarantine: assign cleanup failed", "id", id, "error", delErr)
		} else if filePath != "" {
			if rmErr := os.Remove(filePath); rmErr != nil && !os.IsNotExist(rmErr) {
				slog.Warn("quarantine: assign file removal failed", "path", filePath, "error", rmErr)
			}
		}

		patientName := ""
		if patientExists {
			patientName = strings.TrimSpace(patient.LastName + " " + patient.FirstName)
		}
		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, userID, "quarantine_decision",
			id, map[string]any{
				"action":       "assign",
				"id":           id,
				"patient_id":   req.PatientID,
				"patient_name": patientName,
				"new_patient":  !patientExists,
				"filename":     entry.Filename,
				"vendor":       entry.Vendor,
			})

		return c.JSON(http.StatusOK, map[string]any{
			"id":         id,
			"patient_id": req.PatientID,
			"status":     "assigned",
		})
	}
}
