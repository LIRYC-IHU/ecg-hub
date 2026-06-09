package handlers

import (
	"context"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"github.com/labstack/echo/v4"

	"github.com/LIRYC-IHU/ecg-hub/internal/events"
)

// eventSubscriber is the narrow hub interface needed by the events WS handler.
type eventSubscriber interface {
	Subscribe() (<-chan events.Event, func())
}

// EventsWSHandler handles GET /api/v1/events/ws.
// It upgrades the connection to a WebSocket and streams ingestion events
// (ecg.ingested / ecg.unidentified / ecg.quarantined) pushed by the hub until the
// client disconnects. Auth is enforced by the route middleware (JWT cookie).
func EventsWSHandler(hub eventSubscriber) echo.HandlerFunc {
	return func(c echo.Context) error {
		conn, err := websocket.Accept(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer conn.Close(websocket.StatusInternalError, "")

		sub, unsubscribe := hub.Subscribe()
		defer unsubscribe()

		// Tie the lifetime to the request context. A background reader detects client
		// disconnects (and discards any incoming frames) so we can stop promptly.
		ctx, cancel := context.WithCancel(c.Request().Context())
		defer cancel()
		go func() {
			for {
				if _, _, err := conn.Read(ctx); err != nil {
					cancel()
					return
				}
			}
		}()

		// Heartbeat keeps proxies from idling out the connection between events.
		ping := time.NewTicker(30 * time.Second)
		defer ping.Stop()

		for {
			select {
			case <-ctx.Done():
				conn.Close(websocket.StatusNormalClosure, "")
				return nil
			case <-ping.C:
				pctx, pcancel := context.WithTimeout(ctx, 5*time.Second)
				err := conn.Ping(pctx)
				pcancel()
				if err != nil {
					return nil
				}
			case ev, ok := <-sub:
				if !ok {
					return nil
				}
				wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
				err := wsjson.Write(wctx, conn, ev)
				wcancel()
				if err != nil {
					return nil // client disconnected
				}
			}
		}
	}
}
