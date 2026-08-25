package handlers

import (
	"context"
	"time"

	"connectrpc.com/connect"

	apiv1 "github.com/LIRYC-IHU/ecg-hub/internal/api/v1"
	"github.com/LIRYC-IHU/ecg-hub/internal/events"
)

// eventSubscriber is the narrow hub interface the event stream needs.
type eventSubscriber interface {
	Subscribe() (<-chan events.Event, func())
}

// EventServiceHandler implements apiv1connect.EventServiceHandler. Protected —
// the streaming auth+permission interceptor (mw.ConnectStreamAuth) enforces
// patient.read in RegisterRoutes.
type EventServiceHandler struct {
	Hub eventSubscriber
}

// eventToProto maps an internal hub event to the wire message.
func eventToProto(e events.Event) *apiv1.Event {
	return &apiv1.Event{
		Type:         e.Type,
		EcgId:        e.ECGID,
		PatientId:    e.PatientID,
		QuarantineId: e.QuarantineID,
		Vendor:       e.Vendor,
		Filename:     e.Filename,
		Reason:       e.Reason,
		At:           e.At,
	}
}

// Subscribe streams ingestion events to the client until it disconnects (the
// former GET /api/v1/events/ws WebSocket). A periodic "keepalive" event keeps
// intermediary proxies from idling the long-lived response out between real
// events; the frontend ignores it.
func (h *EventServiceHandler) Subscribe(ctx context.Context, _ *apiv1.SubscribeRequest, stream *connect.ServerStream[apiv1.Event]) error {
	// Tell every intermediary not to buffer this response. A proxy that holds
	// the body until the handler returns turns a live stream into a request that
	// never answers: the upload page waits 5s for the first frame, gives up, and
	// its rows stay on "processing" even though ingestion succeeded.
	//   X-Accel-Buffering — nginx (and ingress controllers built on it)
	//   no-transform      — CDNs, which may otherwise buffer to transform
	stream.ResponseHeader().Set("X-Accel-Buffering", "no")
	stream.ResponseHeader().Set("Cache-Control", "no-cache, no-store, no-transform")

	sub, unsubscribe := h.Hub.Subscribe()
	defer unsubscribe()

	// Send an immediate keepalive: it flushes the response so the client sees the
	// stream is live, and — since it fires only after Subscribe() above — it is a
	// reliable "ready" edge for callers (upload flow) that must not enqueue files
	// before the hub subscription is active, or they'd miss the terminal event.
	if err := stream.Send(&apiv1.Event{Type: "keepalive"}); err != nil {
		return nil
	}

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-keepalive.C:
			if err := stream.Send(&apiv1.Event{Type: "keepalive"}); err != nil {
				return nil // client disconnected
			}
		case ev, ok := <-sub:
			if !ok {
				return nil
			}
			if err := stream.Send(eventToProto(ev)); err != nil {
				return nil // client disconnected
			}
		}
	}
}
