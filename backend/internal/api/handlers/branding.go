package handlers

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
)

const maxLogoBytes = 512 * 1024 // 512 KB

// GetBranding is now served over gRPC/Connect by BrandingServiceHandler
// (branding_service.go). The admin write handlers below stay REST until
// étape 8 of the migration.

// SaveBrandingHandler handles PUT /api/v1/admin/settings/branding.
// Body: { "center_name": "CHU name", "logo_base64": "data:image/png;base64,..." }
// logo_base64 may be empty to keep the existing logo.
func SaveBrandingHandler(repo *repository.ModuleSettingsRepository, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		var body struct {
			CenterName string `json:"center_name"`
			LogoBase64 string `json:"logo_base64"`
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
		actorID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, actorID, "branding_updated", "",
			map[string]any{"center_name": body.CenterName})
		return c.JSON(http.StatusOK, map[string]any{
			"data": map[string]any{
				"center_name": body.CenterName,
			},
		})
	}
}

// UploadLogoHandler handles POST /api/v1/admin/settings/branding/logo.
// Accepts multipart/form-data field "logo" (PNG, JPEG or WebP, max 512 KB).
// Stores as a base64 data URI in the singleton settings row.
func UploadLogoHandler(repo *repository.ModuleSettingsRepository, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		file, err := c.FormFile("logo")
		if err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("BAD_REQUEST", "missing 'logo' field"))
		}

		if file.Size > maxLogoBytes {
			return c.JSON(http.StatusRequestEntityTooLarge, mw.APIError("FILE_TOO_LARGE",
				fmt.Sprintf("logo must be under %d KB", maxLogoBytes/1024)))
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

		// Detect the type from the file's magic bytes — never trust the client's
		// Content-Type or filename. Only raster images are allowed; SVG is rejected
		// because it can carry scripts and the logo is served on the pre-auth login
		// page. DetectContentType reports SVG as text/*, so it fails this check.
		ct := http.DetectContentType(data)
		if !isAllowedImageType(ct) {
			return c.JSON(http.StatusUnsupportedMediaType, mw.APIError("UNSUPPORTED_TYPE", "logo must be a PNG, JPEG or WebP image (SVG is not allowed)"))
		}

		dataURI := fmt.Sprintf("data:%s;base64,%s", ct, base64.StdEncoding.EncodeToString(data))

		centerName, _, err := repo.GetBranding()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		if err := repo.SetBranding(centerName, dataURI); err != nil {
			return c.JSON(http.StatusInternalServerError, mw.APIError("INTERNAL", err.Error()))
		}
		actorID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(c.Request().Context(), db, actorID, "branding_updated", "",
			map[string]any{"logo_updated": true, "content_type": ct})

		return c.JSON(http.StatusOK, map[string]any{
			"data": map[string]any{
				"logo_base64": dataURI,
			},
		})
	}
}

// isAllowedImageType reports whether ct (from http.DetectContentType) is a raster
// image accepted as a logo. SVG is intentionally excluded — it can carry scripts
// and the logo is rendered on the unauthenticated login page.
func isAllowedImageType(ct string) bool {
	return strings.HasPrefix(ct, "image/png") ||
		strings.HasPrefix(ct, "image/jpeg") ||
		strings.HasPrefix(ct, "image/webp")
}
