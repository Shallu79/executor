// Package auth provides password hashing and session-token (JWT) helpers
// for the account endpoints.
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// bcryptCost is a reasonable default (Go's bcrypt cost 10 is the library
// default; 12 is a good security/perf balance).
const bcryptCost = 12

// HashPassword hashes a plaintext password for storage.
func HashPassword(plaintext string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcryptCost)
	return string(b), err
}

// CheckPassword reports whether plaintext matches the stored hash.
func CheckPassword(hash, plaintext string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext)) == nil
}

// SessionTTL is how long a signin token stays valid.
const SessionTTL = 7 * 24 * time.Hour

// NewSessionToken signs an HS256 JWT carrying the user id.
func NewSessionToken(userID, secret string) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   userID,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(SessionTTL)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ParseSessionToken verifies the token and returns the user id.
func ParseSessionToken(tokenStr, secret string) (string, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{},
		func(t *jwt.Token) (any, error) {
			if t.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
			}
			return []byte(secret), nil
		})
	if err != nil {
		return "", err
	}
	claims, ok := token.Claims.(*jwt.RegisteredClaims)
	if !ok || !token.Valid || claims.Subject == "" {
		return "", errors.New("auth: invalid token")
	}
	return claims.Subject, nil
}
