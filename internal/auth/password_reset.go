package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"math/big"
)

const (
	passwordResetOTPLimit        = 1_000_000
	passwordResetTokenByteLength = 32
)

func GeneratePasswordResetOTP() (string, error) {
	number, err := rand.Int(rand.Reader, big.NewInt(passwordResetOTPLimit))
	if err != nil {
		return "", fmt.Errorf("generate password reset OTP: %w", err)
	}

	return fmt.Sprintf("%06d", number.Int64()), nil
}

func HashPasswordResetOTP(otp, pepper string) [sha256.Size]byte {
	mac := hmac.New(sha256.New, []byte(pepper))
	_, _ = mac.Write([]byte(otp))

	var hash [sha256.Size]byte
	copy(hash[:], mac.Sum(nil))
	return hash
}

func PasswordResetOTPMatches(otp, pepper string, expectedHash []byte) bool {
	actualHash := HashPasswordResetOTP(otp, pepper)
	return hmac.Equal(actualHash[:], expectedHash)
}

func GeneratePasswordResetToken() (string, error) {
	randomBytes := make([]byte, passwordResetTokenByteLength)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("generate password reset token: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(randomBytes), nil
}

func HashPasswordResetToken(rawToken string) [sha256.Size]byte {
	return sha256.Sum256([]byte(rawToken))
}

func PasswordResetTokenMatches(rawToken string, expectedHash []byte) bool {
	actualHash := HashPasswordResetToken(rawToken)
	return subtle.ConstantTimeCompare(actualHash[:], expectedHash) == 1
}
