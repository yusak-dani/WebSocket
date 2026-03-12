package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"golang.org/x/time/rate"
)

func main() {
	// Load .env file if exists
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, relying on environment variables.")
	}

	// Initialize Supabase database client
	InitDatabase()

	// Initialize the Lobby Manager
	lobby := NewLobbyManager()
	go lobby.Run()

	// Initialize Rate Limiter (20 requests/sec per IP, burst of 30)
	rateLimiter := NewRateLimiter(rate.Limit(20), 30)

	// Setup allowed origins for CORS/WebSocket
	allowedOrigins := os.Getenv("ALLOWED_ORIGINS") // comma-separated, e.g. "https://superboltz.com,http://localhost"
	if allowedOrigins == "" {
		log.Println("Warning: ALLOWED_ORIGINS not set, allowing all origins (development mode)")
	}

	// Setup Routes
	http.HandleFunc("/ws", RateLimitMiddleware(rateLimiter, func(w http.ResponseWriter, r *http.Request) {
		serveWs(lobby, w, r, allowedOrigins)
	}))

	// Add a simple health check route
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("Super Boltz Multiplayer Server is running!"))
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server listening on :%s\n", port)

	// Log security status
	jwtSecret := os.Getenv("SUPABASE_JWT_SECRET")
	if jwtSecret != "" {
		log.Println("JWT Authentication: ENABLED")
	} else {
		log.Println("JWT Authentication: DISABLED (set SUPABASE_JWT_SECRET to enable)")
	}
	if allowedOrigins != "" {
		log.Printf("CORS Origins: %s\n", allowedOrigins)
	} else {
		log.Println("CORS Origins: ALL (development mode)")
	}

	err = http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

// isOriginAllowed checks if the request origin is in the allowed list
func isOriginAllowed(origin, allowedOrigins string) bool {
	if allowedOrigins == "" {
		return true // Development mode: allow all
	}
	for _, allowed := range strings.Split(allowedOrigins, ",") {
		if strings.TrimSpace(allowed) == origin {
			return true
		}
	}
	return false
}
