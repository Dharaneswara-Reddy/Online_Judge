package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/toji339/online-judge/internal/realtime"
	"github.com/toji339/online-judge/internal/warroom"
)

// WebSocket timings. The server pings idle connections so a dropped
// client is noticed instead of leaking a goroutine and a subscription.
const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

// WarRoomController serves the lobby, the room lifecycle, and the live
// WebSocket each participant holds open during a race.
type WarRoomController struct {
	rooms    *warroom.Service
	bus      realtime.Bus
	upgrader websocket.Upgrader
}

// NewWarRoomController creates the controller. allowedOrigin is the
// frontend origin permitted to open a socket — the browser does not
// apply CORS to WebSockets, so the check has to happen here.
func NewWarRoomController(rooms *warroom.Service, bus realtime.Bus, allowedOrigin string) *WarRoomController {
	return &WarRoomController{
		rooms: rooms,
		bus:   bus,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				// A same-origin request (or a non-browser client) sends no
				// Origin header at all.
				return origin == "" || origin == allowedOrigin
			},
		},
	}
}

// CreateRoom handles POST /api/warrooms (authenticated).
func (wc *WarRoomController) CreateRoom(c *gin.Context) {
	var body struct {
		ProblemSlug     string `json:"problemSlug"`
		MaxParticipants int    `json:"maxParticipants"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request body", "details": err.Error()})
		return
	}

	room, err := wc.rooms.Create(c.Request.Context(), warroom.CreateInput{
		ProblemSlug:     body.ProblemSlug,
		MaxParticipants: body.MaxParticipants,
		Creator: warroom.Participant{
			UserID:   c.GetString("userID"),
			Username: c.GetString("username"),
		},
	})
	if err != nil {
		writeWarRoomError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"success": true, "message": "War room created", "data": room})
}

// ListRooms handles GET /api/warrooms (authenticated), returning rooms
// still waiting for players.
func (wc *WarRoomController) ListRooms(c *gin.Context) {
	rooms, err := wc.rooms.ListOpen(c.Request.Context(), 30)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to list war rooms"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rooms})
}

// ListMyRooms handles GET /api/warrooms/mine (authenticated).
func (wc *WarRoomController) ListMyRooms(c *gin.Context) {
	rooms, err := wc.rooms.ListForUser(c.Request.Context(), c.GetString("userID"), 20)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to list war rooms"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rooms})
}

// GetRoom handles GET /api/warrooms/:code (authenticated).
func (wc *WarRoomController) GetRoom(c *gin.Context) {
	room, err := wc.rooms.GetByCode(c.Request.Context(), c.Param("code"))
	if err != nil {
		writeWarRoomError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": room})
}

// JoinRoom handles POST /api/warrooms/:code/join (authenticated).
// Filling the last seat starts the race and is announced to the room.
func (wc *WarRoomController) JoinRoom(c *gin.Context) {
	participant := warroom.Participant{
		UserID:   c.GetString("userID"),
		Username: c.GetString("username"),
	}

	room, started, err := wc.rooms.Join(c.Request.Context(), c.Param("code"), participant)
	if err != nil {
		writeWarRoomError(c, err)
		return
	}

	// Tell everyone already watching. Broadcasting is best-effort: a
	// missed event must not fail a join that already succeeded.
	wc.broadcast(c, room.ID, warroom.EventParticipantJoined, room)
	if started {
		wc.broadcast(c, room.ID, warroom.EventRoomStarted, room)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Joined war room", "data": room})
}

// broadcast publishes a room event, logging rather than failing on error.
func (wc *WarRoomController) broadcast(c *gin.Context, roomID, eventType string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("warroom: could not encode %s event: %v", eventType, err)
		return
	}
	if err := wc.bus.Publish(c.Request.Context(), roomID, realtime.Event{Type: eventType, Payload: body}); err != nil {
		log.Printf("warroom: could not broadcast %s event: %v", eventType, err)
	}
}

// Live handles GET /ws/warroom/:code (authenticated), the WebSocket a
// participant holds open for the duration of a race.
//
// Events arrive through the bus rather than from this process's memory,
// so a participant connected to a different API instance still sees
// every update.
func (wc *WarRoomController) Live(c *gin.Context) {
	// Steps to follow while opening a live race socket
	// ==================================================

	// 1. Only participants may watch a room
	room, err := wc.rooms.EnsureParticipant(c.Request.Context(), c.Param("code"), c.GetString("userID"))
	if err != nil {
		writeWarRoomError(c, err)
		return
	}

	// 2. Subscribe before upgrading, so a failure can still be reported
	//    as a normal HTTP error rather than a broken socket.
	ctx, cancel := context.WithCancel(context.Background())
	events, unsubscribe, err := wc.bus.Subscribe(ctx, room.ID)
	if err != nil {
		cancel()
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Live updates are unavailable"})
		return
	}

	// 3. Upgrade the connection
	conn, err := wc.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		cancel()
		unsubscribe()
		log.Printf("warroom: websocket upgrade failed: %v", err)
		return
	}

	defer func() {
		cancel()
		unsubscribe()
		conn.Close()
	}()

	// 4. Send the current room state so a client that joined late, or
	//    reconnected, renders correctly without waiting for an event.
	if snapshot, err := json.Marshal(room); err == nil {
		_ = conn.WriteJSON(realtime.Event{Type: warroom.EventParticipantJoined, RoomID: room.ID, Payload: snapshot})
	}

	// 5. Read pongs in the background purely to detect a dead peer
	go func() {
		defer cancel()
		conn.SetReadDeadline(time.Now().Add(pongWait))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(pongWait))
		})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// 6. Pump events out until the client leaves or the race ends
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-events:
			if !ok {
				return
			}
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteJSON(event); err != nil {
				return
			}

		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// writeWarRoomError maps war room service errors to HTTP responses.
func writeWarRoomError(c *gin.Context, err error) {
	var vErr warroom.ValidationError
	switch {
	case errors.As(err, &vErr):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Validation failed", "errors": []string{vErr.Error()}})
	case errors.Is(err, warroom.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "War room not found"})
	case errors.Is(err, warroom.ErrRoomFull):
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
	case errors.Is(err, warroom.ErrRoomClosed):
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
	case errors.Is(err, warroom.ErrNotParticipant):
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "War room request failed"})
	}
}
