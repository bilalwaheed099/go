package auth

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMakeJWT(t *testing.T) {
	testUserID := uuid.New()
	secret := "secret"
	expiry := time.Now().Add(5 * time.Minute).Unix()

	token, err := MakeJWT(testUserID, secret, time.Duration(expiry))
	if err != nil {
		t.Fatalf("Failed to generate jwt")
	}

	if token == "" {
		t.Fatalf("Invalid token generated")
	}
}

func testGetBearerToken(t *testing.T) {
	testString := "Bearer djfliwsefhsdlkdfhlsdkjf"
	headers := http.Header{}
	http.Header.Add(headers, "Authorization", testString)
	_, err := GetBearerToken(headers)
	if err != nil {
		t.Fatalf("Failed to get the token")
	}
}
