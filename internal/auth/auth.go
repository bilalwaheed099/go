package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

func HashPassword(password string) (string, error) {
	hashedPwd, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hashedPwd), nil
}

func CheckPasswordHash(hash, password string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil {
		return err
	}
	return nil
}

func MakeJWT(userID uuid.UUID, tokenSecret string, expiresIn time.Duration) (string, error) {
	log.Printf("expiry: %v", expiresIn)
	now := time.Now().UTC()
	claims := &jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(now.Add(expiresIn)),
		Issuer:    "chirpy",
		IssuedAt:  jwt.NewNumericDate(now),
		Subject:   string(userID.String()),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString([]byte(tokenSecret))
	if err != nil {
		return "", err
	}
	return signedToken, nil
}

type CustomClaims struct {
	jwt.RegisteredClaims
}

func ValidateJWT(tokenString, tokenSecret string) (uuid.UUID, error) {
	claims := &CustomClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("invalid signing method: %v", token.Header["alg"])
		}
		return []byte(tokenSecret), nil
	})
	if err != nil {
		return uuid.Nil, err
	}

	if !token.Valid {
		return uuid.Nil, fmt.Errorf("invalid token")
	}

	id, _ := token.Claims.GetSubject()
	uuid_, _ := uuid.Parse(id)

	return uuid_, nil
}

func GetBearerToken(headers http.Header) (string, error) {
	completeTokenString := headers.Get("Authorization")
	log.Printf("token string %v", completeTokenString)
	if len(completeTokenString) == 0 {
		return "", fmt.Errorf("token invalid")
	}
	tokenString := strings.Split(completeTokenString, " ")[1]
	if len(tokenString) == 0 {
		return "", fmt.Errorf("token invalid")
	}
	return tokenString, nil
}

func MakeRefreshToken() (string, error) {
	randomBytes := make([]byte, 32)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return "", fmt.Errorf("error in creating refresh token: %v", err)
	}

	encodedString := hex.EncodeToString(randomBytes)
	return encodedString, nil
}
