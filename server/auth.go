package main

import (
	"errors"
	"log"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// Claims represents the JWT token payload from Supabase Auth
type Claims struct {
	Sub   string `json:"sub"`   // User ID (UUID)
	Email string `json:"email"` // User email
	jwt.RegisteredClaims
}

// ValidateJWT verifies a Supabase Auth JWT token and returns the claims
func ValidateJWT(tokenString string) (*Claims, error) {
	jwtSecret := os.Getenv("SUPABASE_JWT_SECRET")
	if jwtSecret == "" {
		log.Println("Warning: SUPABASE_JWT_SECRET not set, skipping JWT validation")
		return nil, errors.New("JWT secret not configured")
	}

	// Remove "Bearer " prefix if present
	tokenString = strings.TrimPrefix(tokenString, "Bearer ")

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(jwtSecret), nil
	})

	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}

	return claims, nil
}
