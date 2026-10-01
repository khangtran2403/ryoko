package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestGenerateRefreshToken(t *testing.T) {
	first, err := GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken() first error = %v", err)
	}
	second, err := GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken() second error = %v", err)
	}

	if first == second {
		t.Fatal("GenerateRefreshToken() returned identical tokens")
	}

	for name, token := range map[string]string{
		"first":  first,
		"second": second,
	} {
		t.Run(name, func(t *testing.T) {
			decoded, err := base64.RawURLEncoding.DecodeString(token)
			if err != nil {
				t.Fatalf("token is not valid raw URL-safe base64: %v", err)
			}
			if len(decoded) != refreshTokenRandomBytes {
				t.Errorf("decoded token length = %d, want %d", len(decoded), refreshTokenRandomBytes)
			}
		})
	}
}

func TestHashRefreshToken(t *testing.T) {
	first := HashRefreshToken("first-refresh-token")
	firstAgain := HashRefreshToken("first-refresh-token")
	second := HashRefreshToken("second-refresh-token")

	if first != firstAgain {
		t.Error("HashRefreshToken() is not deterministic")
	}
	if first == second {
		t.Error("HashRefreshToken() returned the same hash for different tokens")
	}
	if len(first) != sha256.Size {
		t.Errorf("hash length = %d, want %d", len(first), sha256.Size)
	}
}
