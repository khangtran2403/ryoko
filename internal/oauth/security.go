package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

const flowRandomByteLength = 32

func GenerateState() (string, error) {
	return generateRandomValue("OAuth state")
}

func GeneratePKCEVerifier() (string, error) {
	return generateRandomValue("PKCE verifier")
}

func PKCEChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func GenerateNonce() (string, error) {
	return generateRandomValue("OpenID nonce")
}

func SecureEqual(first, second string) bool {
	firstHash := sha256.Sum256([]byte(first))
	secondHash := sha256.Sum256([]byte(second))
	return subtle.ConstantTimeCompare(firstHash[:], secondHash[:]) == 1
}

func generateRandomValue(name string) (string, error) {
	randomBytes := make([]byte, flowRandomByteLength)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("generate %s: %w", name, err)
	}
	return base64.RawURLEncoding.EncodeToString(randomBytes), nil
}
