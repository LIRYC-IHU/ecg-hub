package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/device"
)

// DeviceRepository stores the ingestion device whitelist. It implements
// device.Store.
type DeviceRepository struct {
	db *gorm.DB
}

// NewDeviceRepository constructs a new repository.
func NewDeviceRepository(db *gorm.DB) *DeviceRepository {
	return &DeviceRepository{db: db}
}

// Status returns the recorded status for mac, or "" when the device has never
// been seen.
func (r *DeviceRepository) Status(ctx context.Context, mac string) (string, error) {
	var d models.Device
	err := r.db.WithContext(ctx).Select("status").First(&d, "mac = ?", mac).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return d.Status, nil
}

// Get returns one device by MAC.
func (r *DeviceRepository) Get(ctx context.Context, mac string) (*models.Device, error) {
	var d models.Device
	if err := r.db.WithContext(ctx).First(&d, "mac = ?", mac).Error; err != nil {
		return nil, err
	}
	return &d, nil
}

// LabelsFor resolves MAC -> label for the addresses given, skipping the ones
// with no name yet. One query per page of ECGs rather than a join rewritten
// into every list: the lists load the ECG model itself, and turning them into
// scans of a custom row would take the JSONB metadata with it.
//
// Unknown addresses are simply absent from the map — an ECG keeps the device it
// arrived from even after the device is deleted from the inventory, and the
// caller falls back to showing the address.
func (r *DeviceRepository) LabelsFor(ctx context.Context, macs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(macs) == 0 {
		return out, nil
	}
	var rows []struct{ MAC, Label string }
	if err := r.db.WithContext(ctx).Model(&models.Device{}).
		Select("mac", "label").
		Where("mac IN ? AND label != ''", macs).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.MAC] = row.Label
	}
	return out, nil
}

// Seen records a contact. On first sight the row is created with
// initialStatus; afterwards only the volatile columns are refreshed, so an
// operator's approval or revocation is never overwritten by a later
// connection. An empty initialStatus means the caller knows the row exists.
//
// The insert and the update are one statement: two devices reconnecting at
// once would otherwise race between the lookup and the write, and the loser
// would fail on the unique index rather than count its contact.
func (r *DeviceRepository) Seen(ctx context.Context, id device.Identity, initialStatus string) error {
	if id.MAC == "" {
		return nil // nothing to key on; the gate has already decided
	}
	if initialStatus == "" {
		initialStatus = models.DeviceStatusPending
	}
	now := time.Now()
	row := models.Device{
		MAC:         id.MAC,
		OUI:         device.OUI(id.MAC),
		Status:      initialStatus,
		FirstSource: id.Source,
		LastIP:      id.IP,
		FirstSeenAt: now,
		LastSeenAt:  now,
		SeenCount:   1,
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "mac"}},
		DoUpdates: clause.Assignments(map[string]any{
			"last_seen_at": now,
			"last_ip":      id.IP,
			"seen_count":   gorm.Expr("devices.seen_count + 1"),
			"updated_at":   now,
		}),
	}).Create(&row).Error
}

// Describe attaches what a vendor module read out of a file the device sent
// during a pairing window. Only fills blanks and only for a device still
// pending: a later parse must not silently rewrite what an operator approved.
func (r *DeviceRepository) Describe(ctx context.Context, mac, vendor, model, serial string) error {
	updates := map[string]any{}
	if vendor != "" {
		updates["vendor"] = vendor
	}
	if model != "" {
		updates["device_model"] = model
	}
	if serial != "" {
		updates["serial_number"] = serial
	}
	if len(updates) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&models.Device{}).
		Where("mac = ? AND status = ?", mac, models.DeviceStatusPending).
		Updates(updates).Error
}

// List returns devices, newest contact first. An empty status returns all.
func (r *DeviceRepository) List(ctx context.Context, status string) ([]models.Device, error) {
	q := r.db.WithContext(ctx).Order("last_seen_at DESC")
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var out []models.Device
	return out, q.Find(&out).Error
}

// Approve enrols a device. label and description are the operator's, and are
// written even when empty so an approval can clear them.
func (r *DeviceRepository) Approve(ctx context.Context, mac, label, description, by string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&models.Device{}).
		Where("mac = ?", mac).
		Updates(map[string]any{
			"status":         models.DeviceStatusApproved,
			"label":          label,
			"description":    description,
			"approved_by":    by,
			"approved_at":    now,
			"revoked_by":     "",
			"revoked_at":     nil,
			"revoked_reason": "",
		}).Error
}

// Revoke withdraws a device. The row is kept: a deleted device would come back
// as a fresh "pending" on its next connection, losing both the audit trail and
// the reason someone had for shutting it out.
func (r *DeviceRepository) Revoke(ctx context.Context, mac, reason, by string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&models.Device{}).
		Where("mac = ?", mac).
		Updates(map[string]any{
			"status":         models.DeviceStatusRevoked,
			"revoked_by":     by,
			"revoked_at":     now,
			"revoked_reason": reason,
		}).Error
}

// Delete removes a device outright. Use it to clear noise from the pending
// queue; a device that must stay out belongs in Revoke.
func (r *DeviceRepository) Delete(ctx context.Context, mac string) error {
	return r.db.WithContext(ctx).Where("mac = ?", mac).Delete(&models.Device{}).Error
}
