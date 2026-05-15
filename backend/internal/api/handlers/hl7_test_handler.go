package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/hl7"
)

// HL7FullQuerier extends Querier with the full response method.
type HL7FullQuerier interface {
	hl7.Querier
	QueryPatientFull(ctx context.Context, patientID string) (*hl7.QueryResult, error)
}

// HL7TestRequest is the body for POST /admin/hl7/test.
type HL7TestRequest struct {
	PatientID string `json:"patient_id"`
}

// HL7TestResponse is the response from POST /admin/hl7/test.
type HL7TestResponse struct {
	Success      bool                     `json:"success"`
	PatientID    string                   `json:"patient_id"`
	Duration     string                   `json:"duration"`
	Error        string                   `json:"error,omitempty"`
	Raw          string                   `json:"raw,omitempty"`
	Tree         []hl7.SegmentNode        `json:"tree,omitempty"`
	MSA          *HL7MSAResponse          `json:"msa,omitempty"`
	Demographics *HL7DemographicsResponse `json:"demographics,omitempty"`
}

// HL7MSAResponse is the MSA acknowledgment info.
type HL7MSAResponse struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// HL7DemographicsResponse is the parsed patient demographics.
type HL7DemographicsResponse struct {
	LastName    string `json:"last_name"`
	FirstName   string `json:"first_name"`
	DateOfBirth string `json:"date_of_birth"`
	Gender      string `json:"gender"`
	Source      string `json:"source"`
}

// HL7TestHandler handles POST /admin/hl7/test — sends a QRY^A19 and returns the full result with tree.
func HL7TestHandler(client HL7FullQuerier) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req HL7TestRequest
		if err := c.Bind(&req); err != nil || req.PatientID == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": "patient_id is required",
			})
		}

		start := time.Now()
		ctx, cancel := context.WithTimeout(c.Request().Context(), 15*time.Second)
		defer cancel()

		result, err := client.QueryPatientFull(ctx, req.PatientID)
		elapsed := time.Since(start)

		if err != nil && result == nil {
			return c.JSON(http.StatusOK, HL7TestResponse{
				Success:   false,
				PatientID: req.PatientID,
				Duration:  elapsed.Round(time.Millisecond).String(),
				Error:     err.Error(),
			})
		}

		resp := HL7TestResponse{
			Success:   err == nil,
			PatientID: req.PatientID,
			Duration:  elapsed.Round(time.Millisecond).String(),
			Raw:       result.Raw,
			Tree:      result.Tree,
		}

		if err != nil {
			resp.Error = err.Error()
		}

		if result.MSA != nil {
			resp.MSA = &HL7MSAResponse{
				Code:    result.MSA.Code,
				Message: result.MSA.Message,
			}
		}

		if result.Demographics != nil {
			resp.Demographics = &HL7DemographicsResponse{
				LastName:    result.Demographics.LastName,
				FirstName:   result.Demographics.FirstName,
				DateOfBirth: result.Demographics.DateOfBirth,
				Gender:      result.Demographics.Gender,
				Source:      result.Demographics.Source,
			}
		}

		return c.JSON(http.StatusOK, resp)
	}
}

// ─── HL7 Mapping Presets CRUD ────────────────────────────────────────────────

// ListHL7PresetsHandler returns all presets with their mappings.
func ListHL7PresetsHandler(repo *repository.HL7MappingRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		presets, err := repo.ListPresets()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"code": "DB_ERROR", "message": "query failed"})
		}
		type presetWithMappings struct {
			models.HL7MappingPreset
			Mappings []models.HL7Mapping `json:"mappings"`
		}
		var result []presetWithMappings
		for _, p := range presets {
			mappings, _ := repo.ListMappings(p.ID)
			result = append(result, presetWithMappings{HL7MappingPreset: p, Mappings: mappings})
		}
		return c.JSON(http.StatusOK, map[string]any{"data": result})
	}
}

// CreateHL7PresetRequest is the body for POST /admin/hl7/presets.
type CreateHL7PresetRequest struct {
	Name     string `json:"name"`
	Mappings []struct {
		SourcePath  string `json:"source_path"`
		TargetField string `json:"target_field"`
	} `json:"mappings"`
}

// CreateHL7PresetHandler creates a new preset with mappings.
func CreateHL7PresetHandler(repo *repository.HL7MappingRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req CreateHL7PresetRequest
		if err := c.Bind(&req); err != nil || req.Name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"code": "INVALID_PARAMS", "message": "name is required"})
		}

		preset, err := repo.CreatePreset(req.Name)
		if err != nil {
			return c.JSON(http.StatusConflict, map[string]string{"code": "DUPLICATE_NAME", "message": "preset name already exists"})
		}

		var records []models.HL7Mapping
		for _, m := range req.Mappings {
			if m.SourcePath == "" || m.TargetField == "" {
				continue
			}
			records = append(records, models.HL7Mapping{
				SourcePath:  m.SourcePath,
				TargetField: m.TargetField,
			})
		}
		if len(records) > 0 {
			_ = repo.SaveMappings(preset.ID, records)
		}

		return c.JSON(http.StatusCreated, map[string]any{"data": preset})
	}
}

// ActivateHL7PresetHandler sets a preset as active.
func ActivateHL7PresetHandler(repo *repository.HL7MappingRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if err := repo.ActivatePreset(id); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"code": "DB_ERROR", "message": "activate failed"})
		}
		return c.NoContent(http.StatusNoContent)
	}
}

// DeleteHL7PresetHandler deletes a preset and its mappings.
func DeleteHL7PresetHandler(repo *repository.HL7MappingRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if err := repo.DeletePreset(id); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"code": "DB_ERROR", "message": "delete failed"})
		}
		return c.NoContent(http.StatusNoContent)
	}
}

// SaveHL7PresetMappingsRequest is the body for PUT /admin/hl7/presets/:id/mappings.
type SaveHL7PresetMappingsRequest struct {
	Mappings []struct {
		SourcePath  string `json:"source_path"`
		TargetField string `json:"target_field"`
	} `json:"mappings"`
}

// SaveHL7PresetMappingsHandler replaces mappings for a preset.
func SaveHL7PresetMappingsHandler(repo *repository.HL7MappingRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		var req SaveHL7PresetMappingsRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"code": "INVALID_PARAMS", "message": err.Error()})
		}

		var records []models.HL7Mapping
		for _, m := range req.Mappings {
			if m.SourcePath == "" || m.TargetField == "" {
				continue
			}
			records = append(records, models.HL7Mapping{
				SourcePath:  m.SourcePath,
				TargetField: m.TargetField,
			})
		}

		if err := repo.SaveMappings(id, records); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"code": "DB_ERROR", "message": "save failed"})
		}

		return c.JSON(http.StatusOK, map[string]any{"data": records})
	}
}

// GetActiveHL7MappingsHandler returns the currently active mapping configuration.
func GetActiveHL7MappingsHandler(repo *repository.HL7MappingRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		mappings, _ := repo.GetActiveMappings()
		if len(mappings) == 0 {
			return c.JSON(http.StatusOK, map[string]any{"data": []any{}, "active": false})
		}
		return c.JSON(http.StatusOK, map[string]any{"data": mappings, "active": true})
	}
}

// ListHL7AttemptsHandler returns the HL7 attempt history for a given patient.
func ListHL7AttemptsHandler(repo *repository.HL7AttemptRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		patientID := c.Param("id")
		if patientID == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"code":    "INVALID_PARAMS",
				"message": "patient id is required",
			})
		}

		limit := 20
		if l := c.QueryParam("limit"); l != "" {
			if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
				limit = parsed
			}
		}

		attempts, err := repo.ListByPatient(patientID, limit)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"code":    "DB_ERROR",
				"message": "query failed",
			})
		}

		return c.JSON(http.StatusOK, map[string]any{"data": attempts})
	}
}
