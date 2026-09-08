package models

import "time"

// Device statuses. A row exists as soon as a device has been seen once; the
// status is what the gate consults.
const (
	// DeviceStatusPending is a device that has contacted an ingestion port and
	// is waiting for an operator to approve it. It is not allowed to ingest.
	DeviceStatusPending = "pending"
	// DeviceStatusApproved is a device an operator has enrolled. Only these are
	// allowed through when the whitelist is enabled.
	DeviceStatusApproved = "approved"
	// DeviceStatusRevoked is a device an operator has withdrawn. It is kept
	// rather than deleted so the revocation is auditable and so the device does
	// not silently reappear as a fresh "pending" on its next connection.
	DeviceStatusRevoked = "revoked"
)

// Device is one piece of hardware allowed — or not — to push ECGs into the
// ingestion ports (FTP, DICOM C-STORE, ECTP).
//
// The key is the MAC address, not the IP: hospital networks hand out addresses
// by DHCP, so an IP identifies a device only until its lease renews. The MAC is
// resolved from the ARP cache at connection time, which means it is only
// available when the device shares a broadcast domain with the server — see
// internal/device for what happens when it does not.
//
// A MAC is trivially spoofable. This is an enrolment and inventory control, of
// the kind hospital network teams already run on their switches, not an
// authentication boundary.
type Device struct {
	ID string `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`

	// MAC is the normalised lower-case address, "aa:bb:cc:dd:ee:ff". Unique:
	// one row per device, whatever port it arrives on.
	MAC string `gorm:"type:text;not null;uniqueIndex" json:"mac"`
	// OUI is the first three bytes, upper-case, kept alongside so the UI can
	// show a manufacturer prefix without re-deriving it.
	OUI string `gorm:"type:text;not null;default:''" json:"oui"`

	// Status is one of the DeviceStatus* constants.
	Status string `gorm:"type:text;not null;default:'pending';index" json:"status"`

	// Label is the operator's name for the device ("Cardio B, room 214").
	Label string `gorm:"type:text;not null;default:''" json:"label"`
	// Description is free-form context captured at approval time.
	Description string `gorm:"type:varchar(255);not null;default:''" json:"description"`

	// Vendor, DeviceModel and SerialNumber come from an ECG the device sent
	// during a pairing window: the vendor modules already extract them, so an
	// operator approves "Philips PageWriter TC70, SN 1234" rather than a bare
	// MAC. Empty when the device was only ever seen at connection level.
	Vendor       string `gorm:"type:text;not null;default:''" json:"vendor"`
	DeviceModel  string `gorm:"type:text;not null;default:''" json:"device_model"`
	SerialNumber string `gorm:"type:text;not null;default:''" json:"serial_number"`

	// FirstSource is the ingestion port the device first appeared on ("ftp",
	// "dicom", "ectp"). Informational — approval is per device, not per port.
	FirstSource string `gorm:"type:text;not null;default:''" json:"first_source"`
	// LastIP is the address last seen for this MAC. Display only: under DHCP it
	// moves, which is exactly why it is not the key.
	LastIP string `gorm:"type:inet" json:"last_ip"`

	FirstSeenAt time.Time `gorm:"not null" json:"first_seen_at"`
	LastSeenAt  time.Time `gorm:"not null;index" json:"last_seen_at"`
	// SeenCount counts connections, approved or not. A refused device climbing
	// fast is a device someone forgot to enrol — or one that should not be there.
	SeenCount int64 `gorm:"not null;default:0" json:"seen_count"`

	ApprovedBy string     `gorm:"type:text;not null;default:''" json:"approved_by"`
	ApprovedAt *time.Time `json:"approved_at"`

	RevokedBy     string     `gorm:"type:text;not null;default:''" json:"revoked_by"`
	RevokedAt     *time.Time `json:"revoked_at"`
	RevokedReason string     `gorm:"type:varchar(255);not null;default:''" json:"revoked_reason"`

	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (Device) TableName() string { return "devices" }
