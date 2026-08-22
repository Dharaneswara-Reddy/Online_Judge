package controllers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wsReadLimitServer stands up a real WebSocket endpoint configured the
// same way the War Room socket is, and reports what the read pump saw.
func wsReadLimitServer(t *testing.T) (*httptest.Server, <-chan error) {
	t.Helper()

	readErr := make(chan error, 1)
	upgrader := websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 1024}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			readErr <- err
			return
		}
		defer conn.Close()

		configureReadPump(conn)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				readErr <- err
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	return srv, readErr
}

func dialWS(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(srv.URL, "http://", "ws://", 1), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// TestWarRoomSocket_RejectsAnOversizedFrame is the finding: without a
// read limit gorilla buffers a frame of any size, so one authenticated
// participant could exhaust the API's memory. The connection must be
// torn down instead of the payload being allocated.
func TestWarRoomSocket_RejectsAnOversizedFrame(t *testing.T) {
	srv, readErr := wsReadLimitServer(t)
	client := dialWS(t, srv)

	oversized := bytes.Repeat([]byte("A"), maxSocketMessageBytes+1)
	require.NoError(t, client.WriteMessage(websocket.TextMessage, oversized))

	select {
	case err := <-readErr:
		assert.ErrorIs(t, err, websocket.ErrReadLimit,
			"the read pump must refuse the frame rather than buffer it")
	case <-time.After(5 * time.Second):
		t.Fatal("the server never rejected the oversized frame")
	}

	// The client should be told why, with the standard 1009 close code.
	_, _, err := client.ReadMessage()
	assert.True(t, websocket.IsCloseError(err, websocket.CloseMessageTooBig),
		"expected a 1009 close, got %v", err)
}

// TestWarRoomSocket_AcceptsAnOrdinaryControlMessage guards against the
// limit being set so tight it breaks the traffic clients really send.
func TestWarRoomSocket_AcceptsAnOrdinaryControlMessage(t *testing.T) {
	srv, readErr := wsReadLimitServer(t)
	client := dialWS(t, srv)

	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte(`{"type":"ping"}`)))

	select {
	case err := <-readErr:
		t.Fatalf("a normal message was rejected: %v", err)
	case <-time.After(250 * time.Millisecond):
		// Still connected, which is the pass condition.
	}
}
