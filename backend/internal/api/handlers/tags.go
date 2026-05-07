package handlers

import (
	"net/http"

	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

func ListTagsHandler(repo *repository.TagRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		tags, err := repo.ListTags()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to list tags"})
		}
		return c.JSON(http.StatusOK, map[string]any{"data": tags})
	}
}

type createTagBody struct {
	Name  string `json:"name" validate:"required"`
	Color string `json:"color"`
}

func CreateTagHandler(repo *repository.TagRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := c.Get(mw.CtxKeyUserID).(string)
		var body createTagBody
		if err := c.Bind(&body); err != nil || body.Name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "name required"})
		}
		color := body.Color
		if color == "" {
			color = "#6b7280"
		}
		tag, err := repo.CreateTag(body.Name, color, userID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create tag"})
		}
		return c.JSON(http.StatusCreated, map[string]any{"data": tag})
	}
}

type updateTagBody struct {
	Name  string `json:"name" validate:"required"`
	Color string `json:"color" validate:"required"`
}

func UpdateTagHandler(repo *repository.TagRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		var body updateTagBody
		if err := c.Bind(&body); err != nil || body.Name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "name and color required"})
		}
		tag, err := repo.UpdateTag(id, body.Name, body.Color)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "tag not found"})
		}
		return c.JSON(http.StatusOK, map[string]any{"data": tag})
	}
}

func DeleteTagHandler(repo *repository.TagRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		if err := repo.DeleteTag(id); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to delete tag"})
		}
		return c.NoContent(http.StatusNoContent)
	}
}

type tagPatientBody struct {
	TagID string `json:"tag_id" validate:"required"`
}

func TagPatientHandler(repo *repository.TagRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		patientID := c.Param("id")
		var body tagPatientBody
		if err := c.Bind(&body); err != nil || body.TagID == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "tag_id required"})
		}
		if err := repo.TagPatient(patientID, body.TagID); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to tag patient"})
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func UntagPatientHandler(repo *repository.TagRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		patientID := c.Param("id")
		tagID := c.Param("tag_id")
		if err := repo.UntagPatient(patientID, tagID); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to untag patient"})
		}
		return c.NoContent(http.StatusNoContent)
	}
}

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
