package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const refreshTokenRandomBytes = 32

func GenerateRefreshToken() (string, error) {
	randomBytes := make([]byte, refreshTokenRandomBytes)

	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(randomBytes), nil
}

func HashRefreshToken(rawToken string) [sha256.Size]byte {
	return sha256.Sum256([]byte(rawToken))
}
