// auth/auth_test.go
package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestMakeAndValidateJWT_Success(t *testing.T) {
	secret := "test-secret"
	userID := uuid.New()
	exp := 1 * time.Hour // token lives for one hour

	// ---- create the token -------------------------------------------------
	token, err := MakeJWT(userID, secret, exp)
	if err != nil {
		t.Fatalf("MakeJWT returned error: %v", err)
	}
	if token == "" {
		t.Fatalf("MakeJWT returned empty string")
	}

	// ---- validate the token -----------------------------------------------
	gotID, err := ValidateJWT(token, secret)
	if err != nil {
		t.Fatalf("ValidateJWT returned error: %v", err)
	}
	if gotID != userID {
		t.Fatalf("expected UUID %s, got %s", userID, gotID)
	}
}

func TestValidateJWT_Expired(t *testing.T) {
	secret := "test-secret"
	userID := uuid.New()
	// token expires 2 seconds in the past
	exp := -2 * time.Second

	token, err := MakeJWT(userID, secret, exp)
	if err != nil {
		t.Fatalf("MakeJWT failed: %v", err)
	}

	_, err = ValidateJWT(token, secret)
	if err == nil {
		t.Fatalf("expected error for expired token, got nil")
	}
	// The library wraps jwt.ErrTokenExpired, so check both ways
	if !errors.Is(err, jwt.ErrTokenExpired) && !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expiration error, got %v", err)
	}
}

func TestValidateJWT_WrongSecret(t *testing.T) {
	correct := "correct-secret"
	wrong := "wrong-secret"
	userID := uuid.New()
	exp := 5 * time.Minute

	token, err := MakeJWT(userID, correct, exp)
	if err != nil {
		t.Fatalf("MakeJWT failed: %v", err)
	}

	_, err = ValidateJWT(token, wrong)
	if err == nil {
		t.Fatalf("expected signature validation error, got nil")
	}
	if !strings.Contains(err.Error(), "signature") && !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("unexpected error for wrong secret: %v", err)
	}
}

func TestValidateJWT_MissingSubject(t *testing.T) {
	secret := "test-secret"
	// Build a token manually with an empty Subject claim
	now := time.Now().UTC()
	claims := jwt.RegisteredClaims{
		Issuer:    "chirpy",
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)),
		// Subject left empty
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	_, err = ValidateJWT(tokenStr, secret)
	if err == nil {
		t.Fatalf("expected error for missing subject, got nil")
	}
	if !strings.Contains(err.Error(), "subject") {
		t.Fatalf("unexpected error for missing subject: %v", err)
	}
}
