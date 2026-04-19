package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const tokenExpiry = 24 * time.Hour

// Claims represents the JWT claims for an authenticated user.
//
// TokenVersion mirrors the `users.token_version` row at mint time. The
// middleware compares it against the row's current version on every request;
// if the row's version has been bumped (e.g. by ChangePassword) the token
// is rejected as revoked.
type Claims struct {
	UserID       uuid.UUID `json:"sub"`
	Username     string    `json:"username"`
	Role         string    `json:"role"`
	TokenVersion int       `json:"tv"`
	jwt.RegisteredClaims
}

// GenerateToken creates a signed JWT for the given user. tokenVersion must
// match the user row's current `token_version` so the middleware can
// detect later revocation.
func GenerateToken(secret []byte, userID uuid.UUID, username, role string, tokenVersion int) (string, error) {
	claims := Claims{
		UserID:       userID,
		Username:     username,
		Role:         role,
		TokenVersion: tokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(tokenExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("auth: sign token: %w", err)
	}
	return signed, nil
}

// ParseToken validates and parses a JWT token string.
func ParseToken(secret []byte, tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(_ *jwt.Token) (any, error) {
		return secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("auth: parse token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("auth: invalid token")
	}

	return claims, nil
}
