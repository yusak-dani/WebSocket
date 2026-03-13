package main

import (
	"encoding/json"

	"github.com/gorilla/websocket"
)

// Const defining event types
const (
	EventCreateRoom  = "CREATE_ROOM"
	EventJoinRoom    = "JOIN_ROOM"
	EventRoomCreated = "ROOM_CREATED"
	EventRoomJoined  = "ROOM_JOINED"
	EventError       = "ERROR"
	// Game flow
	EventStartGame   = "START_GAME"
	EventGameStarted = "GAME_STARTED"
	EventFinishGame  = "FINISH_GAME"
	EventLeaderboard = "LEADERBOARD_UPDATE"
	EventPlayAgain   = "PLAY_AGAIN"
	EventGameOver    = "GAME_OVER"    // Sent when all players finished, includes final leaderboard
	EventBackToRoom  = "BACK_TO_ROOM" // Server tells clients to go back to room lobby
	EventLeaveRoom   = "LEAVE_ROOM"
	EventPlayerLeft  = "PLAYER_LEFT"
	EventHostChanged = "HOST_CHANGED"
)

// Message is the generic container for WS payload
type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// Payload structs for Unmarshal
type PayloadCreateRoom struct {
	Username string `json:"username"`
	UserID   string `json:"user_id"`
}

type PayloadJoinRoom struct {
	RoomCode string `json:"room_code"`
	Username string `json:"username"`
	UserID   string `json:"user_id"`
}

type PayloadFinishGame struct {
	ScoreTimeMs int `json:"score_time_ms"`
}

// Payload structs for Marshal (Outgoing)
type OutgoingRoomUpdate struct {
	RoomCode string   `json:"room_code"`
	Players  []Player `json:"players"`
	MaxPlayers int    `json:"max_players"`
}

type OutgoingLeaderboard struct {
	Players []PlayerScore `json:"players"`
}

type PlayerScore struct {
	Username    string `json:"username"`
	ScoreTimeMs *int   `json:"score_time_ms,omitempty"`
	Score       *int   `json:"score,omitempty"` // New points field
	IsFinished  bool   `json:"is_finished"`
	Position    int    `json:"position,omitempty"`
}

type OutgoingError struct {
	Message string `json:"message"`
}

// Helper to write message directly to WS (NOT thread-safe — use for pre-player connections only)
func SendWSMessage(conn *websocket.Conn, msgType string, payload interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	msg := Message{
		Type:    msgType,
		Payload: payloadBytes,
	}

	return conn.WriteJSON(msg)
}

// SafeSendWSMessage writes to a Player's connection with mutex protection (thread-safe)
func SafeSendWSMessage(player *Player, msgType string, payload interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	msg := Message{
		Type:    msgType,
		Payload: payloadBytes,
	}

	player.WriteMu.Lock()
	defer player.WriteMu.Unlock()
	return player.Conn.WriteJSON(msg)
}
