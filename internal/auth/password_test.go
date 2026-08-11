package auth

import (
	"strings"
	"testing"
)

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name      string
		password  string
		wantError bool
	}{
		{
			name:      "fewer than fifteen characters",
			password:  strings.Repeat("a", 14),
			wantError: true,
		},
		{
			name:      "exactly fifteen characters",
			password:  strings.Repeat("a", 15),
			wantError: false,
		},
		{
			name:      "exactly seventy two bytes",
			password:  strings.Repeat("a", 72),
			wantError: false,
		},
		{
			name:      "more than seventy two bytes",
			password:  strings.Repeat("a", 73),
			wantError: true,
		},
		{
			name:      "unicode password within byte limit",
			password:  strings.Repeat("界", 15),
			wantError: false,
		},
		{
			name:      "unicode password over byte limit",
			password:  strings.Repeat("界", 25),
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.password)
			if tt.wantError && err == nil {
				t.Fatal("ValidatePassword() error = nil, want an error")
			}
			if !tt.wantError && err != nil {
				t.Fatalf("ValidatePassword() error = %v, want nil", err)
			}
		})
	}
}

func TestHashAndCheckPassword(t *testing.T) {
	password := "a long hotel password"

	firstHash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() first call error = %v", err)
	}
	if firstHash == password {
		t.Fatal("HashPassword() returned the plaintext password")
	}
	if !CheckPasswordHash(password, firstHash) {
		t.Fatal("CheckPasswordHash() rejected the correct password")
	}
	if CheckPasswordHash("the wrong password", firstHash) {
		t.Fatal("CheckPasswordHash() accepted an incorrect password")
	}

	secondHash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() second call error = %v", err)
	}
	if firstHash == secondHash {
		t.Fatal("HashPassword() produced identical hashes for separate calls")
	}
	if !CheckPasswordHash(password, secondHash) {
		t.Fatal("CheckPasswordHash() rejected the correct password for the second hash")
	}
}

func TestCheckPasswordHashRejectsMalformedHash(t *testing.T) {
	if CheckPasswordHash("a long hotel password", "not-a-bcrypt-hash") {
		t.Fatal("CheckPasswordHash() accepted a malformed hash")
	}
}
