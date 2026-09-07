package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/authbridge"
	"github.com/Tutitoos/mailflow/services/api/internal/modules/events"
	fastwebsocket "github.com/fasthttp/websocket"
	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
)

const (
	eventWriteTimeout = 5 * time.Second
	eventPongTimeout  = 45 * time.Second
	eventPingInterval = 20 * time.Second
)

type EventStream interface {
	Replay(context.Context, string, string) ([]events.Envelope, string, error)
	Next(context.Context, string, string) ([]events.Envelope, error)
}

func eventEndpoint(stream EventStream, shutdown context.Context, origin string) fiber.Handler {
	origins := []string(nil)
	if origin != "" {
		origins = []string{origin}
	}
	upgrade := websocket.New(func(connection *websocket.Conn) {
		serveEvents(connection, stream, shutdown)
	}, websocket.Config{
		Origins: origins, AllowEmptyOrigin: true, Subprotocols: []string{"mailflow.v1"},
		HandshakeTimeout: 5 * time.Second, ReadBufferSize: 1024, WriteBufferSize: 32 << 10,
		RecoverHandler: func(connection *websocket.Conn) {
			if recover() != nil {
				_ = connection.WriteControl(fastwebsocket.CloseMessage, fastwebsocket.FormatCloseMessage(fastwebsocket.CloseInternalServerErr, "internal error"), time.Now().Add(eventWriteTimeout))
			}
		},
	})
	return func(c fiber.Ctx) error {
		if stream == nil {
			return newProblem(fiber.StatusServiceUnavailable, "events_unavailable", "Events unavailable", "Real-time events are temporarily unavailable.")
		}
		if !websocket.IsWebSocketUpgrade(c) {
			return newProblem(fiber.StatusUpgradeRequired, "websocket_required", "WebSocket required", "Upgrade this request to WebSocket.")
		}
		user, ok := authbridge.UserFromContext(c.Context())
		if !ok {
			return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
		}
		c.Locals("mailflow.event.user", user.ID)
		c.Locals("mailflow.event.cursor", c.Query("cursor"))
		return upgrade(c)
	}
}

func websocketBearer(c fiber.Ctx) error {
	if c.Path() != "/api/v1/events" || c.Get(fiber.HeaderAuthorization) != "" {
		return c.Next()
	}
	protocols := c.Get(fiber.HeaderSecWebSocketProtocol)
	if len(protocols) > 8<<10 {
		return newProblem(fiber.StatusUnauthorized, "authentication_failed", "Authentication failed", "A valid access token is required.")
	}
	for _, protocol := range strings.Split(protocols, ",") {
		protocol = strings.TrimSpace(protocol)
		if token, found := strings.CutPrefix(protocol, "mailflow.bearer."); found && token != "" {
			c.Request().Header.Set(fiber.HeaderAuthorization, "Bearer "+token)
			break
		}
	}
	return c.Next()
}

func serveEvents(connection *websocket.Conn, stream EventStream, shutdown context.Context) {
	userID, _ := connection.Locals("mailflow.event.user").(string)
	cursor, _ := connection.Locals("mailflow.event.cursor").(string)
	if shutdown == nil {
		shutdown = context.Background()
	}
	ctx, cancel := context.WithCancel(shutdown)
	defer cancel()
	connection.SetReadLimit(1024)
	_ = connection.SetReadDeadline(time.Now().Add(eventPongTimeout))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(eventPongTimeout))
	})
	readDone := make(chan struct{}, 1)
	go func() {
		defer func() { readDone <- struct{}{} }()
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	}()

	replayed, latest, err := stream.Replay(ctx, userID, cursor)
	if errors.Is(err, events.ErrCursorExpired) {
		control := events.Envelope{Version: events.EnvelopeVersion, Cursor: latest, Type: "system.resync_required", Timestamp: time.Now().UTC(), Payload: json.RawMessage(`{"reason":"cursor_expired"}`)}
		if writeEvent(connection, control) != nil {
			return
		}
	} else if err != nil {
		_ = connection.WriteControl(fastwebsocket.CloseMessage, fastwebsocket.FormatCloseMessage(fastwebsocket.ClosePolicyViolation, "invalid cursor"), time.Now().Add(eventWriteTimeout))
		return
	} else {
		for _, event := range replayed {
			if writeEvent(connection, event) != nil {
				return
			}
		}
	}
	cursor = latest
	lastPing := time.Now()
	for {
		select {
		case <-ctx.Done():
			_ = connection.WriteControl(fastwebsocket.CloseMessage, fastwebsocket.FormatCloseMessage(fastwebsocket.CloseGoingAway, "server shutdown"), time.Now().Add(eventWriteTimeout))
			return
		case <-readDone:
			return
		default:
		}
		batch, err := stream.Next(ctx, userID, cursor)
		if err != nil {
			return
		}
		for _, event := range batch {
			if writeEvent(connection, event) != nil {
				return
			}
			cursor = event.Cursor
		}
		if time.Since(lastPing) >= eventPingInterval {
			if err := connection.WriteControl(fastwebsocket.PingMessage, nil, time.Now().Add(eventWriteTimeout)); err != nil {
				return
			}
			lastPing = time.Now()
		}
	}
}

func writeEvent(connection *websocket.Conn, event events.Envelope) error {
	if err := connection.SetWriteDeadline(time.Now().Add(eventWriteTimeout)); err != nil {
		return err
	}
	return connection.WriteJSON(event)
}
