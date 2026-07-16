package handlers

import (
	"os"
	"path/filepath"
)

// dirSize returns the total byte size of all files under path. Shared by
// AdminService.GetStorageMetrics (admin_service.go); the former REST
// VolumeMetricsHandler was removed with the gRPC migration.
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
