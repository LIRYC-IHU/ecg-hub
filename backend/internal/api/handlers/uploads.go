package handlers

import (
	"io"
	"net/http"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
)

// maxUploadFileBytes caps a single uploaded ECG file (50 MiB). Vendor ECG files
// are well under this; the limit guards against accidental huge uploads.
const maxUploadFileBytes = 50 << 20

// uploadFileResult describes the outcome of queueing one uploaded file.
type uploadFileResult struct {
	Filename string `json:"filename"`
	Size     int    `json:"size"`
	Status   string `json:"status"` // "queued" | "rejected"
	Error    string `json:"error,omitempty"`
}

// UploadECGsHandler handles POST /api/v1/uploads.
// It accepts a multipart form with one or more files under the "files" field and
// pushes each onto the shared ingestion queue with Source="upload" — the exact same
// pipeline used by FTP/DICOM. Each file is then routed, parsed, and either stored
// (valid + patient ID), sent to the "unidentified" review queue (valid, no patient
// ID), or quarantined (parse error). Live per-file status is delivered to the UI via
// the existing /api/v1/events/ws WebSocket (correlated by filename).
//
// Requires: AuthMiddleware, RequirePermission(ecg.upload)
//
// @Summary Upload ECG files manually (offline/isolated devices)
// @Description Push one or more ECG files into the ingestion pipeline (same as FTP/DICOM).
// @Description Accessible with an API key (X-API-Key header) so offline/isolated devices and
// @Description scripts can submit files without an interactive login. Requires the ecg.upload
// @Description permission on the key's role. Valid files are stored, files without a patient ID
// @Description go to the unidentified review queue, invalid files are rejected.
// @Tags uploads
// @Accept multipart/form-data
// @Param files formData file true "ECG files (repeat the field for multiple)"
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Security ApiKeyAuth
// @Router /api/v1/uploads [post]
func UploadECGsHandler(queue ingestion.IngestQueue, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {
		form, err := c.MultipartForm()
		if err != nil {
			return c.JSON(http.StatusBadRequest, mw.APIError("INVALID_FORM", "expected multipart/form-data: "+err.Error()))
		}
		files := form.File["files"]
		if len(files) == 0 {
			return c.JSON(http.StatusBadRequest, mw.APIError("NO_FILES", "no files provided (field 'files')"))
		}

		ctx := c.Request().Context()
		results := make([]uploadFileResult, 0, len(files))
		queued := 0

		for _, fh := range files {
			name := filepath.Base(fh.Filename)
			res := uploadFileResult{Filename: name, Size: int(fh.Size)}

			if fh.Size <= 0 {
				res.Status, res.Error = "rejected", "empty file"
				results = append(results, res)
				continue
			}
			if fh.Size > maxUploadFileBytes {
				res.Status, res.Error = "rejected", "file exceeds 50 MiB limit"
				results = append(results, res)
				continue
			}

			f, err := fh.Open()
			if err != nil {
				res.Status, res.Error = "rejected", "cannot read file"
				results = append(results, res)
				continue
			}
			data, err := io.ReadAll(io.LimitReader(f, maxUploadFileBytes))
			f.Close()
			if err != nil || len(data) == 0 {
				res.Status, res.Error = "rejected", "cannot read file"
				results = append(results, res)
				continue
			}

			item := ingestion.IngestItem{Filename: name, Data: data, Source: "upload"}
			// Blocking send (unlike FTP's drop-on-full): the client is waiting for a
			// per-file outcome, so we never silently drop. Abort if the client goes away.
			select {
			case queue <- item:
				res.Status = "queued"
				queued++
			case <-ctx.Done():
				res.Status, res.Error = "rejected", "request cancelled"
				results = append(results, res)
				return c.JSON(http.StatusOK, map[string]any{"files": results, "queued": queued})
			}
			results = append(results, res)
		}

		// Audit — non-blocking is unnecessary here; the queue push already happened.
		userID, _ := c.Get(mw.CtxKeyUserID).(string)
		_ = mw.WriteAuditLog(ctx, db, userID, "ecg_upload", "", map[string]any{
			"file_count": len(files),
			"queued":     queued,
		})

		return c.JSON(http.StatusOK, map[string]any{
			"files":  results,
			"queued": queued,
		})
	}
}
