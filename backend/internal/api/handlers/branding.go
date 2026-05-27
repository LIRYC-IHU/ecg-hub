package handlers

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

const maxLogoBytes = 512 * 1024 // 512 KB

// GetBrandingHandler handles GET /api/v1/branding.
// Public — used by login and setup pages before authentication.
func GetBrandingHandler(repo *repository.ModuleSettingsRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		centerName, logoBase64, err := repo.GetBranding()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		return c.JSON(http.StatusOK, map[string]any{
			"data": map[string]any{
				"center_name": centerName,
				"logo_base64": logoBase64,
			},
		})
	}
}

// SaveBrandingHandler handles PUT /api/v1/admin/settings/branding.
// Body: { "center_name": "IHU Liryc — Bordeaux", "logo_base64": "data:image/png;base64,..." }
// logo_base64 may be empty to keep the existing logo.
func SaveBrandingHandler(repo *repository.ModuleSettingsRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		var body struct {
			CenterName  string `json:"center_name"`
			LogoBase64  string `json:"logo_base64"`
		}
		if err := c.Bind(&body); err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "invalid body"))
		}

		// If logo omitted, keep existing.
		logo := body.LogoBase64
		if logo == "" {
			_, existing, err := repo.GetBranding()
			if err != nil {
				return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
			}
			logo = existing
		}

		if err := repo.SetBranding(body.CenterName, logo); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		return c.JSON(http.StatusOK, map[string]any{
			"data": map[string]any{
				"center_name": body.CenterName,
			},
		})
	}
}

// UploadLogoHandler handles POST /api/v1/admin/settings/branding/logo.
// Accepts multipart/form-data field "logo" (PNG, JPEG, SVG, max 512 KB).
// Stores as a base64 data URI in the singleton settings row.
func UploadLogoHandler(repo *repository.ModuleSettingsRepository) echo.HandlerFunc {
	return func(c echo.Context) error {
		file, err := c.FormFile("logo")
		if err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "missing 'logo' field"))
		}

		if file.Size > maxLogoBytes {
			return c.JSON(http.StatusRequestEntityTooLarge, mw.APIError("FILE_TOO_LARGE",
				fmt.Sprintf("logo must be under %d KB", maxLogoBytes/1024)))
		}

		ct := file.Header.Get("Content-Type")
		if !isAllowedImageType(ct) {
			// Sniff from filename if browser didn't set content-type.
			ct = sniffFromFilename(file.Filename)
		}
		if ct == "" {
			return c.JSON(http.StatusUnsupportedMediaType, mw.APIError("UNSUPPORTED_TYPE", "logo must be PNG, JPEG, or SVG"))
		}

		src, err := file.Open()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", "cannot open upload"))
		}
		defer src.Close()

		data, err := io.ReadAll(src)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", "cannot read upload"))
		}

		dataURI := fmt.Sprintf("data:%s;base64,%s", ct, base64.StdEncoding.EncodeToString(data))

		centerName, _, err := repo.GetBranding()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		if err := repo.SetBranding(centerName, dataURI); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}

		return c.JSON(http.StatusOK, map[string]any{
			"data": map[string]any{
				"logo_base64": dataURI,
			},
		})
	}
}

func isAllowedImageType(ct string) bool {
	switch ct {
	case "image/png", "image/jpeg", "image/jpg", "image/svg+xml", "image/webp":
		return true
	}
	return false
}

func sniffFromFilename(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	}
	return ""
}
