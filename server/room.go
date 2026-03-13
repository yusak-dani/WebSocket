package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Player represents a connection in the room (Lobby)
type Player struct {
	Conn        *websocket.Conn `json:"-"`
	WriteMu     sync.Mutex      `json:"-"` // Protects concurrent writes to Conn
	Username    string          `json:"username"`
	UserID      string          `json:"user_id"`
	IsHost      bool            `json:"is_host"`
	IsReady     bool            `json:"is_ready"`
	ScoreTimeMs *int            `json:"score_time_ms,omitempty"`
	IsFinished  bool            `json:"is_finished"`
	JoinedAt    time.Time       `json:"joined_at"`
}

// GameRoom represents an active match
type GameRoom struct {
	mu            sync.Mutex
	Code          string
	Players       map[*websocket.Conn]*Player // Active players indexed by their connection
	MaxPlayers    int
	Status        string // "waiting", "playing", "finished"
	Manager       *LobbyManager
	GameStartTime time.Time // Server-side timer (anti-cheat)
}

// LobbyManager orchestrates all rooms
type LobbyManager struct {
	mu    sync.Mutex
	Rooms map[string]*GameRoom
}

func NewLobbyManager() *LobbyManager {
	return &LobbyManager{
		Rooms: make(map[string]*GameRoom),
	}
}

func (m *LobbyManager) Run() {
	// Cleanup idle rooms every 5 minutes
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		for code, room := range m.Rooms {
			room.mu.Lock()
			if len(room.Players) == 0 {
				delete(m.Rooms, code)
				log.Printf("Room %s cleaned up (idle/empty)", code)
			}
			room.mu.Unlock()
		}
		m.mu.Unlock()
	}
}

// RemoveRoom deletes a room from the manager
func (m *LobbyManager) RemoveRoom(code string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Rooms, code)
	log.Printf("Room %s destroyed (empty)", code)
}

// Handle incoming websocket connections
func serveWs(lobby *LobbyManager, w http.ResponseWriter, r *http.Request, allowedOrigins string) {
	// CORS: Create upgrader with origin check
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			return isOriginAllowed(origin, allowedOrigins)
		},
	}

	// JWT Authentication (optional: only enforced if SUPABASE_JWT_SECRET is set)
	var authenticatedUserID string
	var authenticatedUsername string

	jwtSecret := os.Getenv("SUPABASE_JWT_SECRET")
	if jwtSecret != "" {
		tokenStr := r.URL.Query().Get("token")
		if tokenStr == "" {
			http.Error(w, "Missing authentication token", http.StatusUnauthorized)
			return
		}

		claims, err := ValidateJWT(tokenStr)
		if err != nil {
			log.Printf("JWT validation failed: %v", err)
			http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
			return
		}

		authenticatedUserID = claims.Sub
		authenticatedUsername = claims.Email // Use email as fallback username
		log.Printf("Authenticated user: %s (%s)", authenticatedUserID, authenticatedUsername)
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	// Pass authenticated info to the client handler
	go handleClientRoutine(lobby, conn, authenticatedUserID, authenticatedUsername)
}

// --- Input Validation Helpers ---

var validRoomCodeRegex = regexp.MustCompile(`^[A-Za-z0-9]{4,6}$`)
var validUsernameRegex = regexp.MustCompile(`^[A-Za-z0-9_\- ]{1,50}$`)

func validateUsername(username string) error {
	if username == "" {
		return errors.New("username is required")
	}
	if len(username) > 50 {
		return errors.New("username too long (max 50 chars)")
	}
	if !validUsernameRegex.MatchString(username) {
		return errors.New("username contains invalid characters")
	}
	return nil
}

func validateRoomCode(code string) error {
	if code == "" {
		return errors.New("room code is required")
	}
	if !validRoomCodeRegex.MatchString(code) {
		return fmt.Errorf("invalid room code format: %s", code)
	}
	return nil
}

func handleClientRoutine(lobby *LobbyManager, conn *websocket.Conn, authUserID, authUsername string) {
	var currentRoom *GameRoom
	var currentPlayer *Player

	defer func() {
		if currentRoom != nil && currentPlayer != nil {
			currentRoom.RemovePlayer(currentPlayer)
		}
		conn.Close()
	}()

	for {
		var msg Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			// Client disconnected — defer will handle RemovePlayer
			break
		}

		// Handle various message types
		switch msg.Type {
		case EventCreateRoom:
			var pl PayloadCreateRoom
			if err := json.Unmarshal(msg.Payload, &pl); err == nil {
				// Use authenticated identity if available, otherwise use payload
				username := pl.Username
				userID := pl.UserID
				if authUserID != "" {
					userID = authUserID
				}
				if authUsername != "" && username == "" {
					username = authUsername
				}

				// Validate input
				if err := validateUsername(username); err != nil {
					SendWSMessage(conn, EventError, OutgoingError{Message: err.Error()})
					continue
				}

				currentRoom, currentPlayer = lobby.CreateRoom(conn, username, userID)
			}
		case EventJoinRoom:
			var pl PayloadJoinRoom
			if err := json.Unmarshal(msg.Payload, &pl); err == nil {
				username := pl.Username
				userID := pl.UserID
				if authUserID != "" {
					userID = authUserID
				}
				if authUsername != "" && username == "" {
					username = authUsername
				}

				// Validate input
				if err := validateUsername(username); err != nil {
					SendWSMessage(conn, EventError, OutgoingError{Message: err.Error()})
					continue
				}
				if err := validateRoomCode(pl.RoomCode); err != nil {
					SendWSMessage(conn, EventError, OutgoingError{Message: err.Error()})
					continue
				}

				room, player, joinErr := lobby.JoinRoom(conn, pl.RoomCode, username, userID)
				if joinErr != nil {
					SendWSMessage(conn, EventError, OutgoingError{Message: joinErr.Error()})
				} else {
					currentRoom = room
					currentPlayer = player
				}
			}
		// Game flow logic
		case EventStartGame:
			// Only Host can start the game AND all players must be ready
			if currentPlayer != nil && currentPlayer.IsHost && currentRoom != nil {
				if err := currentRoom.StartGame(); err != nil {
					SendWSMessage(conn, EventError, OutgoingError{Message: err.Error()})
				}
			}
		case EventFinishGame:
			// Server calculates time — client payload is ignored (anti-cheat)
			if currentRoom != nil && currentPlayer != nil {
				currentRoom.PlayerFinished(currentPlayer)
			}
		case EventPlayAgain:
			// Only Host can trigger play again
			if currentPlayer != nil && currentPlayer.IsHost && currentRoom != nil {
				currentRoom.ResetForNewRound()
			}
		case EventLeaveRoom:
			if currentRoom != nil && currentPlayer != nil {
				currentRoom.RemovePlayer(currentPlayer)
				currentRoom = nil
				currentPlayer = nil
			}
		case EventPlayerReady:
			if currentRoom != nil && currentPlayer != nil {
				currentRoom.ToggleReady(currentPlayer)
			}
		default:
			log.Println("Unknown message type:", msg.Type)
		}
	}
}

func (m *LobbyManager) CreateRoom(conn *websocket.Conn, username, userID string) (*GameRoom, *Player) {
	// Generate random 4-char code with collision check
	var code string
	for {
		bytes := make([]byte, 2)
		rand.Read(bytes)
		code = strings.ToUpper(hex.EncodeToString(bytes))

		m.mu.Lock()
		if _, exists := m.Rooms[code]; !exists {
			break // Code is unique, keep the lock for insertion below
		}
		m.mu.Unlock()
		// Code collision detected, try again
	}

	room := &GameRoom{
		Code:       code,
		Players:    make(map[*websocket.Conn]*Player),
		MaxPlayers: 4, // Set to 4 as requested
		Status:     "waiting",
		Manager:    m,
	}

	player := &Player{
		Conn:     conn,
		Username: username,
		UserID:   userID,
		IsHost:   true,
		JoinedAt: time.Now(),
	}

	room.Players[conn] = player
	m.Rooms[code] = room
	m.mu.Unlock()

	log.Printf("Room %s created by %s", code, username)

	// Send confirmation to client
	room.BroadcastRoomState()

	return room, player
}

func (m *LobbyManager) JoinRoom(conn *websocket.Conn, code, username, userID string) (*GameRoom, *Player, error) {
	m.mu.Lock()
	room, exists := m.Rooms[code]
	m.mu.Unlock()

	if !exists {
		return nil, nil, errors.New("Room not found")
	}

	room.mu.Lock()

	if len(room.Players) >= room.MaxPlayers {
		room.mu.Unlock()
		return nil, nil, errors.New("Room is full")
	}

	if room.Status != "waiting" {
		room.mu.Unlock()
		return nil, nil, errors.New("Game already started")
	}

	player := &Player{
		Conn:     conn,
		Username: username,
		UserID:   userID,
		IsHost:   false,
		JoinedAt: time.Now(),
	}

	room.Players[conn] = player
	log.Printf("Player %s joined Room %s", username, code)
	room.mu.Unlock()

	// Broadcast update to all players — safely outside the lock
	room.BroadcastRoomState()

	return room, player, nil
}

// RemovePlayer handles a player leaving the room
func (r *GameRoom) RemovePlayer(playerToRemove *Player) {
	r.mu.Lock()
	
	// Ensure player exists in room securely
	if _, exists := r.Players[playerToRemove.Conn]; !exists {
		r.mu.Unlock()
		return
	}

	delete(r.Players, playerToRemove.Conn)
	isHostLeaving := playerToRemove.IsHost
	remainingCount := len(r.Players)

	log.Printf("Room %s: Player %s left. Remaining: %d", r.Code, playerToRemove.Username, remainingCount)

	// If no players left, destroy the room
	if remainingCount == 0 {
		r.mu.Unlock()
		r.Manager.RemoveRoom(r.Code)
		return
	}

	// If the host left, find the oldest remaining player
	var newHost *Player
	if isHostLeaving {
		var oldestTime time.Time
		for _, p := range r.Players {
			// Find the earliest JoinedAt time (empty Time is zero value)
			if newHost == nil || p.JoinedAt.Before(oldestTime) {
				newHost = p
				oldestTime = p.JoinedAt
			}
		}

		if newHost != nil {
			newHost.IsHost = true
			log.Printf("Room %s: Host transferred to %s", r.Code, newHost.Username)
		}
	}
	r.mu.Unlock()

	// Notify remaining players
	r.BroadcastRoomState()
	
	// If host changed, we might want to let the new host know specifically
	if isHostLeaving && newHost != nil {
		SafeSendWSMessage(newHost, EventHostChanged, map[string]string{
			"message": "You are now the Host!",
		})
	}
}

func (r *GameRoom) BroadcastRoomState() {
	r.mu.Lock()
	// Collect players to send
	var playerList []Player
	var players []*Player
	for _, p := range r.Players {
		playerList = append(playerList, *p) // Value copy for JSON
		players = append(players, p)
	}
	r.mu.Unlock()

	update := OutgoingRoomUpdate{
		RoomCode:   r.Code,
		Players:    playerList,
		MaxPlayers: r.MaxPlayers,
	}

	for _, p := range players {
		SafeSendWSMessage(p, EventRoomJoined, update)
	}
}

func (r *GameRoom) ToggleReady(player *Player) {
	r.mu.Lock()
	if r.Status != "waiting" {
		r.mu.Unlock()
		return
	}

	player.IsReady = !player.IsReady
	r.mu.Unlock()

	r.BroadcastRoomState()
}

func (r *GameRoom) StartGame() error {
	r.mu.Lock()
	if r.Status == "playing" {
		r.mu.Unlock()
		return errors.New("game already playing")
	}

	// Check if all players are ready
	for _, p := range r.Players {
		if !p.IsReady {
			r.mu.Unlock()
			return fmt.Errorf("player %s is not ready", p.Username)
		}
	}

	r.Status = "playing"
	r.GameStartTime = time.Now() // Record server-side start time
	players := make([]*Player, 0, len(r.Players))
	for _, p := range r.Players {
		players = append(players, p)
	}
	r.mu.Unlock()

	log.Printf("Room %s: Game Started (server time: %v)", r.Code, r.GameStartTime)

	for _, p := range players {
		SafeSendWSMessage(p, EventGameStarted, nil)
	}
	return nil
}

func (r *GameRoom) PlayerFinished(player *Player) {
	r.mu.Lock()
	if r.Status != "playing" || player.IsFinished {
		r.mu.Unlock()
		return // Ignore if game is not playing or already finished
	}

	// Server calculates elapsed time (anti-cheat: ignores client-side time)
	elapsed := int(time.Since(r.GameStartTime).Milliseconds())
	player.ScoreTimeMs = &elapsed
	player.IsFinished = true

	log.Printf("Room %s: Player %s finished in %d ms (server-calculated)", r.Code, player.Username, elapsed)

	// Check if all players are finished
	allFinished := true
	for _, p := range r.Players {
		if !p.IsFinished {
			allFinished = false
			break
		}
	}

	if allFinished {
		r.Status = "finished"
		log.Printf("Room %s: Game Finished! All players done.", r.Code)
	}
	r.mu.Unlock()

	// Always trigger a leaderboard update when someone finishes (In-Lobby Leaderboard)
	r.BroadcastLeaderboard()

	// If all finished, save results to Supabase in a separate goroutine
	if allFinished {
		r.mu.Lock()
		playersCopy := make(map[string]*Player)
		for _, p := range r.Players {
			playersCopy[p.UserID] = p
		}
		roomCode := r.Code
		r.mu.Unlock()

		go SaveMatchResults(roomCode, playersCopy)
	}
}

func (r *GameRoom) BroadcastLeaderboard() {
	r.mu.Lock()
	var leaderboard []PlayerScore
	var players []*Player

	for _, p := range r.Players {
		var calculatedScore *int
		if p.ScoreTimeMs != nil {
			score := 100000 - *p.ScoreTimeMs
			if score < 0 {
				score = 0
			}
			calculatedScore = &score
		}

		leaderboard = append(leaderboard, PlayerScore{
			Username:    p.Username,
			ScoreTimeMs: p.ScoreTimeMs,
			Score:       calculatedScore,
			IsFinished:  p.IsFinished,
		})
		players = append(players, p)
	}
	r.mu.Unlock()

	// Sort leaderboard: finished players first, then by time ascending
	sort.Slice(leaderboard, func(i, j int) bool {
		if leaderboard[i].IsFinished && !leaderboard[j].IsFinished {
			return true
		}
		if !leaderboard[i].IsFinished && leaderboard[j].IsFinished {
			return false
		}
		if leaderboard[i].ScoreTimeMs != nil && leaderboard[j].ScoreTimeMs != nil {
			return *leaderboard[i].ScoreTimeMs < *leaderboard[j].ScoreTimeMs
		}
		return false
	})

	// Add position numbers
	for i := range leaderboard {
		pos := i + 1
		leaderboard[i].Position = pos
	}

	msg := OutgoingLeaderboard{Players: leaderboard}

	for _, p := range players {
		SafeSendWSMessage(p, EventLeaderboard, msg)
	}
}

// ResetForNewRound clears all player scores and sets room back to "waiting" state
func (r *GameRoom) ResetForNewRound() {
	r.mu.Lock()
	if r.Status != "finished" {
		r.mu.Unlock()
		return // Can only reset after game is finished
	}

	// Reset all player states
	for _, p := range r.Players {
		p.ScoreTimeMs = nil
		p.IsFinished = false
		p.IsReady = false // Reset readiness
	}
	r.Status = "waiting"

	players := make([]*Player, 0, len(r.Players))
	for _, p := range r.Players {
		players = append(players, p)
	}
	r.mu.Unlock()

	log.Printf("Room %s: Reset for new round", r.Code)

	// Notify all clients to go back to room lobby
	r.BroadcastRoomState()

	// Send explicit BACK_TO_ROOM event so client knows to switch UI
	for _, p := range players {
		SafeSendWSMessage(p, EventBackToRoom, nil)
	}
}


