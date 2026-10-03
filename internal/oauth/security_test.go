package oauth

import (
	"encoding/base64"
	"testing"
)

func TestGenerateOAuthSecurityValues(t *testing.T) {
	generators := map[string]func() (string, error){
		"state":         GenerateState,
		"PKCE verifier": GeneratePKCEVerifier,
		"nonce":         GenerateNonce,
	}

	for name, generate := range generators {
		t.Run(name, func(t *testing.T) {
			first, err := generate()
			if err != nil {
				t.Fatalf("first generation error = %v", err)
			}
			second, err := generate()
			if err != nil {
				t.Fatalf("second generation error = %v", err)
			}
			if first == second {
				t.Fatal("generated identical values")
			}
			for _, value := range []string{first, second} {
				decoded, err := base64.RawURLEncoding.DecodeString(value)
				if err != nil {
					t.Fatalf("value is not raw URL-safe base64: %v", err)
				}
				if len(decoded) != flowRandomByteLength {
					t.Errorf("decoded length = %d, want %d", len(decoded), flowRandomByteLength)
				}
			}
		})
	}
}

func TestPKCEChallengeRFC7636Example(t *testing.T) {
	const (
		verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
		want     = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	)
	if got := PKCEChallenge(verifier); got != want {
		t.Errorf("PKCEChallenge() = %q, want %q", got, want)
	}
}

func TestSecureEqual(t *testing.T) {
	if !SecureEqual("same-value", "same-value") {
		t.Error("SecureEqual() rejected equal values")
	}
	if SecureEqual("first-value", "second-value") {
		t.Error("SecureEqual() accepted different values")
	}
	if SecureEqual("short", "a-much-longer-value") {
		t.Error("SecureEqual() accepted different-length values")
	}
}
