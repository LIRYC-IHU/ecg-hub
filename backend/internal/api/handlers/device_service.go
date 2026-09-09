package handlers

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"gorm.io/gorm"

	mw "github.com/LIRYC-IHU/ecg-hub/internal/api/middleware"
	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/models"
	"github.com/LIRYC-IHU/ecg-hub/internal/db/repository"
	"github.com/LIRYC-IHU/ecg-hub/internal/device"
	"github.com/LIRYC-IHU/ecg-hub/internal/events"
	"github.com/LIRYC-IHU/ecg-hub/internal/ingestion"
)

// deviceLabelMaxLen and deviceReasonMaxLen bound the free text an operator
// types. They match the varchar the columns declare, so a value that fits here
// fits there.
const (
	deviceLabelMaxLen  = 255
	deviceReasonMaxLen = 255
)

// DevicePairingStore is the narrow view of device.Pairing this handler needs:
// approving a device hands back the file it sent to identify itself.
type DevicePairingStore interface {
	Has(mac string) bool
	Take(mac string) (device.Held, bool)
	Drop(mac string)
}

// DeviceSettingsStore reads and writes the whitelist configuration.
// Implemented by the module-settings repository.
type DeviceSettingsStore interface {
	DeviceSettings(ctx context.Context) (device.Settings, error)
	SetDeviceSettings(enabled, pairingOpen bool, pairingUntil time.Time) error
}

// DeviceIdentityHealth reports what the gate has observed about whether
// hardware can be identified on this deployment. Implemented by device.Gate.
type DeviceIdentityHealth interface {
	Health() device.Health
}

// DeviceServiceHandler implements apiv1connect.DeviceServiceHandler. Reads
// require device.read, mutations device.manage — enforced by the interceptors
// in RegisterRoutes.
type DeviceServiceHandler struct {
	Repo     *repository.DeviceRepository
	Settings DeviceSettingsStore
	Pairing  DevicePairingStore
	Resolver DeviceIdentityHealth
	// Queue re-ingests the file a device sent while pairing, once approved.
	Queue ingestion.IngestQueue
	// Hub carries device events to the pairing screen; nil disables the stream.
	Hub eventSubscriber
	Pub interface{ Publish(events.Event) }
	DB  *gorm.DB
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func rfc3339Ptr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return rfc3339(*t)
}

func (h *DeviceServiceHandler) deviceToProto(d *models.Device) *apiv1.Device {
	out := &apiv1.Device{
		Id:            d.ID,
		Mac:           d.MAC,
		Oui:           d.OUI,
		Status:        d.Status,
		Label:         d.Label,
		Description:   d.Description,
		Vendor:        d.Vendor,
		DeviceModel:   d.DeviceModel,
		SerialNumber:  d.SerialNumber,
		FirstSource:   d.FirstSource,
		LastIp:        d.LastIP,
		FirstSeenAt:   rfc3339(d.FirstSeenAt),
		LastSeenAt:    rfc3339(d.LastSeenAt),
		SeenCount:     d.SeenCount,
		ApprovedBy:    d.ApprovedBy,
		ApprovedAt:    rfc3339Ptr(d.ApprovedAt),
		RevokedBy:     d.RevokedBy,
		RevokedAt:     rfc3339Ptr(d.RevokedAt),
		RevokedReason: d.RevokedReason,
	}
	if h.Pairing != nil && d.Status == models.DeviceStatusPending {
		out.HasHeldFile = h.Pairing.Has(d.MAC)
	}
	return out
}

// ListDevices returns the whitelist, newest contact first.
func (h *DeviceServiceHandler) ListDevices(ctx context.Context, req *apiv1.ListDevicesRequest) (*apiv1.ListDevicesResponse, error) {
	devices, err := h.Repo.List(ctx, strings.TrimSpace(req.Status))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to list devices"))
	}
	out := make([]*apiv1.Device, len(devices))
	for i := range devices {
		out[i] = h.deviceToProto(&devices[i])
	}
	return &apiv1.ListDevicesResponse{Devices: out}, nil
}

// GetSettings returns the whitelist configuration and whether this deployment
// can identify hardware at all.
func (h *DeviceServiceHandler) GetSettings(ctx context.Context, _ *apiv1.GetDeviceSettingsRequest) (*apiv1.GetDeviceSettingsResponse, error) {
	set, err := h.Settings.DeviceSettings(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to read device settings"))
	}
	// The computed value, not the stored flag: an expired window is closed, and
	// a screen still showing it open would be an indicator that lies.
	resp := &apiv1.GetDeviceSettingsResponse{
		Settings: &apiv1.DeviceSettings{
			Enabled:      set.Enabled,
			PairingOpen:  set.PairingActive(time.Now()),
			PairingUntil: rfc3339(set.PairingUntil),
		},
	}
	if h.Resolver != nil {
		health := h.Resolver.Health()
		resp.Degraded = health.Degraded
		resp.IdentifiedConnections = health.Resolved
		resp.UnidentifiedConnections = health.Unresolved
	}
	return resp, nil
}

// UpdateSettings stores the whitelist configuration.
func (h *DeviceServiceHandler) UpdateSettings(ctx context.Context, req *apiv1.UpdateDeviceSettingsRequest) (*apiv1.UpdateDeviceSettingsResponse, error) {
	in := req.Settings
	if in == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("settings are required"))
	}
	var until time.Time
	if s := strings.TrimSpace(in.PairingUntil); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("pairing_until must be an RFC3339 timestamp"))
		}
		until = t
	}
	// Opening the window without an expiry closes it in half an hour rather
	// than never. The default belongs here and not in the browser: it is the
	// only place every caller goes through, and "forever" should take saying
	// so, not forgetting to.
	if in.PairingOpen && until.IsZero() {
		until = time.Now().Add(device.DefaultPairingWindow)
	}
	if err := h.Settings.SetDeviceSettings(in.Enabled, in.PairingOpen, until); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to store device settings"))
	}

	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "device_whitelist_settings", "",
		map[string]any{
			"enabled":       in.Enabled,
			"pairing_open":  in.PairingOpen,
			"pairing_until": rfc3339(until),
		})

	return &apiv1.UpdateDeviceSettingsResponse{Settings: &apiv1.DeviceSettings{
		Enabled:      in.Enabled,
		PairingOpen:  in.PairingOpen,
		PairingUntil: rfc3339(until),
	}}, nil
}

// ApproveDevice enrols a device and, when the device sent a file to identify
// itself, pushes that file back into the ingestion pipeline — so enrolling does
// not cost the ward a second acquisition.
func (h *DeviceServiceHandler) ApproveDevice(ctx context.Context, req *apiv1.ApproveDeviceRequest) (*apiv1.ApproveDeviceResponse, error) {
	mac := device.NormalizeMAC(req.Mac)
	if mac == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("mac must be a hardware address"))
	}
	label, err := boundedText(req.Label, deviceLabelMaxLen, "label")
	if err != nil {
		return nil, err
	}
	description, err := boundedText(req.Description, deviceLabelMaxLen, "description")
	if err != nil {
		return nil, err
	}

	if _, err := h.Repo.Get(ctx, mac); err != nil {
		return nil, deviceLookupError(err)
	}
	if err := h.Repo.Approve(ctx, mac, label, description, mw.UsernameFromContext(ctx)); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to approve device"))
	}

	reingested := h.reingestHeld(mac)

	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "device_approved", mac,
		map[string]any{"label": label, "reingested": reingested})
	h.publish(events.TypeDeviceApproved, mac)

	d, err := h.Repo.Get(ctx, mac)
	if err != nil {
		return nil, deviceLookupError(err)
	}
	return &apiv1.ApproveDeviceResponse{Device: h.deviceToProto(d), Reingested: reingested}, nil
}

// reingestHeld pushes the pairing file back into the pipeline. A full queue
// does not fail the approval: the device is enrolled either way, and the ward
// re-sends at worst one ECG.
func (h *DeviceServiceHandler) reingestHeld(mac string) bool {
	if h.Pairing == nil || h.Queue == nil {
		return false
	}
	held, ok := h.Pairing.Take(mac)
	if !ok {
		return false
	}
	select {
	case h.Queue <- ingestion.IngestItem{
		Filename:  held.Filename,
		Data:      held.Data,
		Source:    held.Identity.Source,
		DeviceMAC: mac,
	}:
		return true
	default:
		return false
	}
}

// RevokeDevice withdraws a device. The row is kept so the revocation and its
// reason survive; a deleted device would return as a fresh pending one.
func (h *DeviceServiceHandler) RevokeDevice(ctx context.Context, req *apiv1.RevokeDeviceRequest) (*apiv1.RevokeDeviceResponse, error) {
	mac := device.NormalizeMAC(req.Mac)
	if mac == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("mac must be a hardware address"))
	}
	reason, err := boundedText(req.Reason, deviceReasonMaxLen, "reason")
	if err != nil {
		return nil, err
	}
	if _, err := h.Repo.Get(ctx, mac); err != nil {
		return nil, deviceLookupError(err)
	}
	if err := h.Repo.Revoke(ctx, mac, reason, mw.UsernameFromContext(ctx)); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to revoke device"))
	}
	if h.Pairing != nil {
		h.Pairing.Drop(mac)
	}

	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "device_revoked", mac,
		map[string]any{"reason": reason})
	h.publish(events.TypeDeviceRevoked, mac)

	d, err := h.Repo.Get(ctx, mac)
	if err != nil {
		return nil, deviceLookupError(err)
	}
	return &apiv1.RevokeDeviceResponse{Device: h.deviceToProto(d)}, nil
}

// DeleteDevice removes a device outright. For clearing noise out of the pending
// queue — a device that must stay out belongs in RevokeDevice, whose row is
// what keeps it out.
func (h *DeviceServiceHandler) DeleteDevice(ctx context.Context, req *apiv1.DeleteDeviceRequest) (*apiv1.DeleteDeviceResponse, error) {
	mac := device.NormalizeMAC(req.Mac)
	if mac == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("mac must be a hardware address"))
	}
	if err := h.Repo.Delete(ctx, mac); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to delete device"))
	}
	if h.Pairing != nil {
		h.Pairing.Drop(mac)
	}
	_ = mw.WriteAuditLog(ctx, h.DB, mw.UserIDFromContext(ctx), "device_deleted", mac, nil)
	return &apiv1.DeleteDeviceResponse{}, nil
}

// SubscribeDevices streams device events until the client disconnects. A device
// is enrolled by plugging it in and sending one ECG, so the pairing screen has
// to be live rather than reloaded.
func (h *DeviceServiceHandler) SubscribeDevices(ctx context.Context, _ *apiv1.SubscribeDevicesRequest, stream *connect.ServerStream[apiv1.DeviceEvent]) error {
	if h.Hub == nil {
		return connect.NewError(connect.CodeUnimplemented, errors.New("realtime events are disabled"))
	}
	// Same reasoning as EventService: an intermediary that buffers the body
	// turns a live stream into a request that never answers.
	stream.ResponseHeader().Set("X-Accel-Buffering", "no")
	stream.ResponseHeader().Set("Cache-Control", "no-cache, no-store, no-transform")

	sub, unsubscribe := h.Hub.Subscribe()
	defer unsubscribe()

	if err := stream.Send(&apiv1.DeviceEvent{Type: "keepalive"}); err != nil {
		return nil
	}
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-keepalive.C:
			if err := stream.Send(&apiv1.DeviceEvent{Type: "keepalive"}); err != nil {
				return nil
			}
		case ev, ok := <-sub:
			if !ok {
				return nil
			}
			if !events.IsDevice(ev.Type) {
				continue
			}
			out := &apiv1.DeviceEvent{Type: ev.Type, At: ev.At}
			// The event carries the MAC only; the row is what the screen shows.
			if d, err := h.Repo.Get(ctx, ev.DeviceMAC); err == nil {
				out.Device = h.deviceToProto(d)
			}
			if err := stream.Send(out); err != nil {
				return nil
			}
		}
	}
}

func (h *DeviceServiceHandler) publish(eventType, mac string) {
	if h.Pub == nil {
		return
	}
	h.Pub.Publish(events.Event{Type: eventType, DeviceMAC: mac})
}

// boundedText trims and length-checks one operator-supplied field.
func boundedText(s string, max int, field string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New(field+" is too long"))
	}
	return s, nil
}

func deviceLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return connect.NewError(connect.CodeNotFound, errors.New("device not found"))
	}
	return connect.NewError(connect.CodeInternal, errors.New("failed to read device"))
}
