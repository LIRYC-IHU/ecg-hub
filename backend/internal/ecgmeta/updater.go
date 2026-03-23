// Package ecgmeta defines editable ECG metadata fields and the generic interface
// for updating source files. Each vendor format implements FileUpdater and registers
// itself via Register() in an init() function.
package ecgmeta

// FileUpdater updates the metadata fields in a vendor-specific source file on disk.
// Implementations must be safe to call concurrently.
type FileUpdater interface {
	// Vendor returns the vendor name (matches models.ECG.Vendor), e.g. "philips".
	Vendor() string

	// UpdateFile applies the given field values to the file at filePath.
	// Only keys present in the map are updated — absent keys are left unchanged.
	// Values are always strings; implementations are responsible for type conversion.
	// An empty string value means "clear the field" — implementations may skip
	// fields that cannot be meaningfully cleared.
	UpdateFile(filePath string, fields map[string]string) error
}

var updaters = map[string]FileUpdater{}

// Register adds u to the global file-updater registry. Call from init().
func Register(u FileUpdater) { updaters[u.Vendor()] = u }

// Get returns the FileUpdater for vendor, or (nil, false) if none is registered.
func Get(vendor string) (FileUpdater, bool) {
	u, ok := updaters[vendor]
	return u, ok
}
