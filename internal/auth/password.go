package auth

import (
	"fmt"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < 15 || len([]byte(password)) > 72 {
		return fmt.Errorf("password must be at least 15 characters long and no more than 72 characters long")
	}
	return nil
}
func HashPassword(password string) (string, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password error: %w", err)
	}
	return string(hashedPassword), nil
}
func CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil {
		return false
	}
	return true
}
