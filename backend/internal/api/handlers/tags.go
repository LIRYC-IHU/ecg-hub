package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

// Tag CRUD and tag/untag moved to TagService (gRPC). Only these two read-only
// listing routes are still served over REST.

func ListPatientTagsHandler(repo *repository.TagRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		patientID := c.Param("id")
		tags, err := repo.ListPatientTags(patientID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list patient tags"})
		}
		return c.JSON(http.StatusOK, map[string]any{"data": tags})
	}
}

func ListECGTagsHandler(repo *repository.TagRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		ecgID := c.Param("id")
		tags, err := repo.ListECGTags(ecgID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list ecg tags"})
		}
		return c.JSON(http.StatusOK, map[string]any{"data": tags})
	}
}
