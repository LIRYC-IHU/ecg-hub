package handlers

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/LIRYC-IHU/ecg-hub/internal/config"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

type VolumeMetrics struct {
	Name      string `json:"name"`
	Total     int64  `json:"total"`
	Available int64  `json:"available"`
	// MaxSize is the raw Kubernetes resource quantity configured in storage.max_size
	// (e.g. "50Gi"). Empty when rotation is disabled. The frontend renders this verbatim
	// so the displayed cap matches what the operator wrote in config.yaml.
	MaxSize string `json:"max_size,omitempty"`
}

type StorageMetricsResp struct {
	Volumes []VolumeMetrics `json:"volumes"`
	Error   string          `json:"error"`
}

// VolumeMetricsHandler  handles GET /api/v1/admin/storage-metrics
// Returns storage usage metrics for all configured volumes (quarantine, ECG storage, etc).
//
// @Summary Storage volume metrics
// @Tags Admin
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /api/v1/admin/storage-metrics [get]
func VolumeMetricsHandler(cfg *config.Config, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {

		var volumes []VolumeMetrics
		storage := cfg.Storage

		storage_size, err := dirSize(storage.VolumePath)
		if err != nil {
			return c.JSON(http.StatusOK, StorageMetricsResp{
				Error: "error getting storage metrics",
			})
		}

		// log info about the volume metrics
		volumes = append(volumes, VolumeMetrics{
			Name:      "ECG Storage",
			Total:     storage.GetBytesSize(),
			Available: storage.GetBytesSize() - storage_size,
			MaxSize:   storage.MaxSize,
		})

		quarantine_size, err := dirSize(storage.QuarantinePath)
		if err != nil {
			return c.JSON(http.StatusOK, StorageMetricsResp{
				Error: "error getting storage metrics",
			})
		}
		volumes = append(volumes, VolumeMetrics{
			Name:      "Quarantine",
			Total:     storage.GetBytesSize(),
			Available: storage.GetBytesSize() - quarantine_size,
			MaxSize:   storage.MaxSize,
		})
		return c.JSON(http.StatusOK, StorageMetricsResp{
			Volumes: volumes,
			Error:   "",
		})
	}

}

func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return err
	})
	return size, err
}
