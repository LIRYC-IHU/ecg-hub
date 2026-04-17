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
}

type StorageMetricsResp struct {
	Volumes []VolumeMetrics `json:"volumes"`
}

func gbToBytes(gb int) int64 {
	return int64(gb) * 1024 * 1024 * 1024
}

// VolumeMetricsHandler  handles GET /api/v1/admin/storage-metrics
// Returns storage usage metrics for all configured volumes (quarantine, ECG storage, etc).
func VolumeMetricsHandler(cfg *config.Config, db *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {

		var volumes []VolumeMetrics
		storage := cfg.Storage

		storage_size, err := dirSize(storage.VolumePath)
		if err != nil {
			return c.JSON(http.StatusOK, echo.Map{"error": "Failed to get storage metrics"})
		}

		// log info about the volume metrics
		volumes = append(volumes, VolumeMetrics{
			Name:      "ECG Storage",
			Total:     gbToBytes(int(storage.MaxSizeGB)),
			Available: (gbToBytes(storage.MaxSizeGB) - storage_size),
		})

		quarantine_size, err := dirSize(storage.QuarantinePath)
		if err != nil {
			return c.JSON(http.StatusOK, echo.Map{"error": "Failed to get storage metrics"})
		}
		volumes = append(volumes, VolumeMetrics{
			Name:      "Quarantine",
			Total:     gbToBytes(int(storage.MaxSizeGB)),
			Available: (gbToBytes(storage.MaxSizeGB) - quarantine_size),
		})
		return c.JSON(http.StatusOK, StorageMetricsResp{
			Volumes: volumes,
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
