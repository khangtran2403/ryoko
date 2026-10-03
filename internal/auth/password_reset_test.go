package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"testing"
)

func TestGeneratePasswordResetOTP(t *testing.T) {
	digitsOnly := regexp.MustCompile(`^[0-9]{6}$`)
	seen := make(map[string]struct{})

	for range 20 {
		otp, err := GeneratePasswordResetOTP()
		if err != nil {
			t.Fatalf("GeneratePasswordResetOTP() error = %v", err)
		}
		if !digitsOnly.MatchString(otp) {
			t.Fatalf("GeneratePasswordResetOTP() = %q, want exactly six digits", otp)
		}
		seen[otp] = struct{}{}
	}

	if len(seen) == 1 {
		t.Fatal("GeneratePasswordResetOTP() returned the same OTP every time")
	}
}

func TestHashPasswordResetOTP(t *testing.T) {
	const (
		otp       = "012345"
		pepper    = "first-password-reset-pepper"
		newOTP    = "543210"
		newPepper = "second-password-reset-pepper"
	)

	hash := HashPasswordResetOTP(otp, pepper)
	sameHash := HashPasswordResetOTP(otp, pepper)
	differentOTPHash := HashPasswordResetOTP(newOTP, pepper)
	differentPepperHash := HashPasswordResetOTP(otp, newPepper)

	if hash != sameHash {
		t.Error("HashPasswordResetOTP() is not deterministic")
	}
	if hash == differentOTPHash {
		t.Error("HashPasswordResetOTP() returned the same hash for different OTPs")
	}
	if hash == differentPepperHash {
		t.Error("HashPasswordResetOTP() returned the same hash for different peppers")
	}
	if len(hash) != sha256.Size {
		t.Errorf("hash length = %d, want %d", len(hash), sha256.Size)
	}
}

func TestPasswordResetOTPMatches(t *testing.T) {
	const (
		otp    = "012345"
		pepper = "password-reset-pepper"
	)
	hash := HashPasswordResetOTP(otp, pepper)

	tests := []struct {
		name         string
		candidateOTP string
		pepper       string
		expectedHash []byte
		want         bool
	}{
		{
			name:         "matching OTP",
			candidateOTP: otp,
			pepper:       pepper,
			expectedHash: hash[:],
			want:         true,
		},
		{
			name:         "incorrect OTP",
			candidateOTP: "654321",
			pepper:       pepper,
			expectedHash: hash[:],
			want:         false,
		},
		{
			name:         "incorrect pepper",
			candidateOTP: otp,
			pepper:       "incorrect-pepper",
			expectedHash: hash[:],
			want:         false,
		},
		{
			name:         "malformed expected hash",
			candidateOTP: otp,
			pepper:       pepper,
			expectedHash: []byte("too-short"),
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PasswordResetOTPMatches(tt.candidateOTP, tt.pepper, tt.expectedHash); got != tt.want {
				t.Errorf("PasswordResetOTPMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGeneratePasswordResetToken(t *testing.T) {
	first, err := GeneratePasswordResetToken()
	if err != nil {
		t.Fatalf("GeneratePasswordResetToken() first error = %v", err)
	}
	second, err := GeneratePasswordResetToken()
	if err != nil {
		t.Fatalf("GeneratePasswordResetToken() second error = %v", err)
	}

	if first == second {
		t.Fatal("GeneratePasswordResetToken() returned identical tokens")
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
			if len(decoded) != passwordResetTokenByteLength {
				t.Errorf("decoded token length = %d, want %d", len(decoded), passwordResetTokenByteLength)
			}
		})
	}
}

func TestHashPasswordResetToken(t *testing.T) {
	first := HashPasswordResetToken("first-password-reset-token")
	firstAgain := HashPasswordResetToken("first-password-reset-token")
	second := HashPasswordResetToken("second-password-reset-token")

	if first != firstAgain {
		t.Error("HashPasswordResetToken() is not deterministic")
	}
	if first == second {
		t.Error("HashPasswordResetToken() returned the same hash for different tokens")
	}
	if len(first) != sha256.Size {
		t.Errorf("hash length = %d, want %d", len(first), sha256.Size)
	}
}

func TestPasswordResetTokenMatches(t *testing.T) {
	const rawToken = "password-reset-token"
	hash := HashPasswordResetToken(rawToken)

	tests := []struct {
		name         string
		rawToken     string
		expectedHash []byte
		want         bool
	}{
		{name: "matching token", rawToken: rawToken, expectedHash: hash[:], want: true},
		{name: "incorrect token", rawToken: "incorrect-token", expectedHash: hash[:], want: false},
		{name: "malformed hash", rawToken: rawToken, expectedHash: []byte("too-short"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PasswordResetTokenMatches(tt.rawToken, tt.expectedHash); got != tt.want {
				t.Errorf("PasswordResetTokenMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}
