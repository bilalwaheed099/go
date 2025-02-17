package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"servers/internal/auth"
	"servers/internal/database"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

type apiConfig struct {
	fileserverHits atomic.Int32
	db             *database.Queries
	platform       string
	secret         string
}

type Chirp struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	UserID    uuid.UUID `json:"user_id"`
	Body      string    `json:"body"`
}

type User struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Email     string    `json:"email"`
}
type UserWithToken struct {
	ID           uuid.UUID `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Email        string    `json:"email"`
	Token        string    `json:"token"`
	RefreshToken string    `json:"refresh_token"`
}

func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileserverHits.Add(1)
		next.ServeHTTP(w, r)
	})
}

var metricsMarkup string = `
	<html>
		<body>
    	<h1>Welcome, Chirpy Admin</h1>
    	<p>Chirpy has been visited %d times!</p>
  	</body>
	</html>
	`

func (cfg *apiConfig) metricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(fmt.Sprintf(metricsMarkup, cfg.fileserverHits.Load())))
}

func (cfg *apiConfig) resetHandler(w http.ResponseWriter, r *http.Request) {
	if cfg.platform != "dev" {
		w.WriteHeader(403)
		w.Write([]byte("Forbidden"))
		return
	}

	cfg.db.DeleteUsers(r.Context())
	cfg.fileserverHits.Store(0)
}

func clean(text string) string {
	textArr := strings.Split(text, " ")
	checkArr := []string{"kerfuffle", "sharbert", "fornax"}
	for i, word := range textArr {
		for _, checkWord := range checkArr {
			if word == checkWord || strings.ToLower(word) == checkWord {
				textArr[i] = "****"
			}
		}
	}
	return strings.Join(textArr, " ")
}

func main() {
	godotenv.Load()
	dbURL := os.Getenv("DB_URL")
	platform := os.Getenv("PLATFORM")
	secret := os.Getenv("SECRET")
	db, _ := sql.Open("postgres", dbURL)
	dbQueries := database.New(db)
	mux := http.NewServeMux()

	apiCfg := apiConfig{
		db:       dbQueries,
		platform: platform,
		secret:   secret,
	}

	mux.Handle("/app/", http.StripPrefix("/app", apiCfg.middlewareMetricsInc(http.FileServer(http.Dir(".")))))
	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		type loginRequestParams struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}

		params := loginRequestParams{}
		decoder := json.NewDecoder(r.Body)
		decoder.Decode(&params)

		// get the user from the email.. includes the password
		user, err := apiCfg.db.GetUserFromEmail(r.Context(), params.Email)
		if err != nil {
			w.WriteHeader(401)
			w.Write([]byte("Incorrect email or password"))
			return
		}

		err = auth.CheckPasswordHash(user.HashedPassword, params.Password)
		if err != nil {
			w.WriteHeader(401)
			w.Write([]byte("Incorrect email or password"))
			return
		}

		refreshToken, _ := auth.MakeRefreshToken()

		apiCfg.db.AddRefreshToken(r.Context(), database.AddRefreshTokenParams{refreshToken, user.ID, time.Now().Add(144 * time.Hour)})

		// JWT token
		tokenExpiry := 3600
		token, _ := auth.MakeJWT(user.ID, apiCfg.secret, time.Duration(tokenExpiry*int(time.Second)))
		userToReturn := UserWithToken{
			ID:           user.ID,
			CreatedAt:    user.CreatedAt,
			UpdatedAt:    user.UpdatedAt,
			Email:        user.Email,
			Token:        token,
			RefreshToken: refreshToken,
		}

		w.WriteHeader(200)
		data, _ := json.Marshal(userToReturn)
		w.Write(data)
	})
	mux.HandleFunc("POST /api/refresh", func(w http.ResponseWriter, r *http.Request) {
		// validing jwt
		log.Printf("***: %v", r.Header.Get("Authorization"))
		token, tokenErr := auth.GetBearerToken(r.Header)
		if tokenErr != nil {
			log.Printf("%v", tokenErr)
			w.WriteHeader(401)
			w.Write([]byte("invalid token"))
			return
		}
		refreshToken, err := apiCfg.db.GetUserFromRefreshToken(r.Context(), token)
		if err != nil {
			w.WriteHeader(401)
			w.Write([]byte("invalid token - does not exist"))
			return
		}
		if refreshToken.ExpiresAt.Before(time.Now()) {
			w.WriteHeader(401)
			w.Write([]byte("invalid token - expired"))
			return
		}

		if refreshToken.RevokedAt.Valid {
			w.WriteHeader(401)
			w.Write([]byte("invalid token - revoked"))
			return
		}
		accessToken, _ := auth.MakeJWT(refreshToken.UserID, apiCfg.secret, time.Duration(3600*time.Second))

		type tokenResponse struct {
			Token string `json:"token"`
		}

		w.WriteHeader(200)
		resp := tokenResponse{
			Token: accessToken,
		}
		data, _ := json.Marshal(resp)
		w.Write(data)
	})
	mux.HandleFunc("POST /api/revoke", func(w http.ResponseWriter, r *http.Request) {
		// validing jwt
		token, tokenErr := auth.GetBearerToken(r.Header)
		if tokenErr != nil {
			log.Printf("%v", tokenErr)
			w.Write([]byte("invalid refresh token"))
			return
		}

		// revoke token
		apiCfg.db.RevokeToken(r.Context(), token)
		w.WriteHeader(204)
		w.Write([]byte("token revoked"))
	})
	mux.HandleFunc("PUT /api/users", func(w http.ResponseWriter, r *http.Request) {
		type parameters struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}

		token, tokenErr := auth.GetBearerToken(r.Header)
		if tokenErr != nil {
			log.Printf("%v", tokenErr)
			w.WriteHeader(401)
			w.Write([]byte("invalid token"))
			return
		}

		userID, validationErr := auth.ValidateJWT(token, apiCfg.secret)
		if validationErr != nil {
			log.Printf("%v", validationErr)
			w.WriteHeader(401)
			w.Write([]byte("invalid token"))
			return
		}

		params := parameters{}
		decoder := json.NewDecoder(r.Body)
		err := decoder.Decode(&params)

		type errorResp struct {
			Error string `json:"error"`
		}

		if err != nil {
			errorResponse := errorResp{
				Error: "Invalid params",
			}
			w.WriteHeader(400)
			data, _ := json.Marshal(errorResponse)
			w.Write(data)
			return
		}

		hashedPassword, err := auth.HashPassword(params.Password)
		if err != nil {
			w.WriteHeader(500)
			w.Write([]byte("Something went wrong while hashing password"))
		}

		// creating the new user in DB
		user, err := apiCfg.db.UpdateUser(r.Context(), database.UpdateUserParams{Email: params.Email, HashedPassword: hashedPassword, ID: userID})
		if err != nil {
			errorResponse := errorResp{
				Error: fmt.Sprintf("Error in updating the user: %v", err),
			}
			w.WriteHeader(500)
			data, _ := json.Marshal(errorResponse)
			w.Write(data)
			return
		}

		userToReturn := User{
			ID:        user.ID,
			CreatedAt: user.CreatedAt,
			UpdatedAt: user.UpdatedAt,
			Email:     user.Email,
		}

		w.WriteHeader(200)
		data, _ := json.Marshal(userToReturn)
		w.Write(data)
	})
	mux.HandleFunc("GET /admin/metrics", apiCfg.metricsHandler)
	mux.HandleFunc("POST /admin/reset", apiCfg.resetHandler)
	mux.HandleFunc("GET /api/chirps", func(w http.ResponseWriter, r *http.Request) {
		chirps, _ := apiCfg.db.GetChirps(r.Context())
		apiChirps := make([]Chirp, len(chirps))
		for i, chirp := range chirps {
			apiChirps[i] = Chirp{
				ID:        chirp.ID,
				CreatedAt: chirp.CreatedAt,
				UpdatedAt: chirp.UpdatedAt,
				Body:      chirp.Body,
				UserID:    chirp.UserID,
			}
		}

		w.WriteHeader(200)
		data, _ := json.Marshal(apiChirps)
		w.Write(data)
	})
	mux.HandleFunc("GET /api/chirps/{ID}", func(w http.ResponseWriter, r *http.Request) {
		chirpID := r.PathValue("ID")
		UUID, _ := uuid.Parse(chirpID)
		chirp, err := apiCfg.db.GetChirp(r.Context(), UUID)
		if err != nil {
			w.WriteHeader(404)
			w.Write([]byte("Chirp not found"))
		}

		chirpToReturn := Chirp{
			ID:        chirp.ID,
			CreatedAt: chirp.CreatedAt,
			UpdatedAt: chirp.UpdatedAt,
			Body:      chirp.Body,
			UserID:    chirp.UserID,
		}

		data, _ := json.Marshal(chirpToReturn)
		w.WriteHeader(200)
		w.Write(data)
	})
	mux.HandleFunc("POST /api/chirps", func(w http.ResponseWriter, r *http.Request) {
		type parameters struct {
			Body string `json:"body"`
		}

		type errorResp struct {
			Error string `json:"error"`
		}

		type cleanedResp struct {
			CleanedBody string `json:"cleaned_body"`
		}

		decoder := json.NewDecoder(r.Body)
		params := parameters{}
		err := decoder.Decode(&params)

		// validing jwt

		token, tokenErr := auth.GetBearerToken(r.Header)
		if tokenErr != nil {
			log.Printf("%v", tokenErr)
			w.Write([]byte("invalid token"))
			return
		}

		userID, validationErr := auth.ValidateJWT(token, apiCfg.secret)
		if validationErr != nil {
			log.Printf("%v", validationErr)
			w.WriteHeader(401)
			w.Write([]byte("invalid token"))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(500)
			errorResponse := errorResp{
				Error: "Something went wrong",
			}
			data, _ := json.Marshal(errorResponse)
			w.Write(data)
			return
		}

		isValid := len(params.Body) <= 140

		if !isValid {
			w.WriteHeader(400)
			errorResponse := errorResp{
				Error: "Chirp is too long",
			}
			data, _ := json.Marshal(errorResponse)
			w.Write(data)
			return
		}

		chirp, err := apiCfg.db.CreateChirp(r.Context(), database.CreateChirpParams{Body: params.Body, UserID: userID})
		chirpToReturn := Chirp{
			ID:        chirp.ID,
			CreatedAt: chirp.CreatedAt,
			UpdatedAt: chirp.UpdatedAt,
			UserID:    chirp.UserID,
			Body:      chirp.Body,
		}

		w.WriteHeader(201)
		//cleanedResponse := cleanedResp{
		//	CleanedBody: clean(params.Body),
		//}

		data, _ := json.Marshal(chirpToReturn)
		w.Write(data)
	})
	mux.HandleFunc("DELETE /api/chirps/{ID}", func(w http.ResponseWriter, r *http.Request) {
		// verifying the token
		token, tokenErr := auth.GetBearerToken(r.Header)
		if tokenErr != nil {
			log.Printf("%v", tokenErr)
			w.WriteHeader(401)
			w.Write([]byte("invalid token"))
			return
		}

		userID, validationErr := auth.ValidateJWT(token, apiCfg.secret)
		if validationErr != nil {
			log.Printf("%v", validationErr)
			w.WriteHeader(401)
			w.Write([]byte("invalid token"))
			return
		}

		// fetching the chirp
		chirpID := r.PathValue("ID")
		UUID, _ := uuid.Parse(chirpID)
		chirp, err := apiCfg.db.GetChirp(r.Context(), UUID)
		if err != nil {
			w.WriteHeader(404)
			w.Write([]byte("Chirp not found"))
			return
		}

		// check if the user is the owner of the token
		if chirp.UserID != userID {
			w.WriteHeader(403)
			w.Write([]byte("Unauthorized"))
			return
		}

		err = apiCfg.db.DeleteChirp(r.Context(), UUID)
		if err != nil {
			w.WriteHeader(500)
			w.Write([]byte("Server Error - something went wrong"))
			return
		}

		w.WriteHeader(204)
		w.Write([]byte("chirp deleted successfully"))
	})

	mux.HandleFunc("POST /api/users", func(w http.ResponseWriter, r *http.Request) {
		type parameters struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}

		params := parameters{}
		decoder := json.NewDecoder(r.Body)
		err := decoder.Decode(&params)

		type errorResp struct {
			Error string `json:"error"`
		}

		if err != nil {
			errorResponse := errorResp{
				Error: "Invalid params",
			}
			w.WriteHeader(400)
			data, _ := json.Marshal(errorResponse)
			w.Write(data)
			return
		}

		hashedPassword, err := auth.HashPassword(params.Password)
		if err != nil {
			w.WriteHeader(500)
			w.Write([]byte("Something went wrong while hashing password"))
		}

		// creating the new user in DB
		user, err := apiCfg.db.CreateUser(r.Context(), database.CreateUserParams{Email: params.Email, HashedPassword: hashedPassword})
		if err != nil {
			errorResponse := errorResp{
				Error: fmt.Sprintf("Error in creating the user: %v", err),
			}
			w.WriteHeader(500)
			data, _ := json.Marshal(errorResponse)
			w.Write(data)
			return
		}

		userToReturn := User{
			ID:        user.ID,
			CreatedAt: user.CreatedAt,
			UpdatedAt: user.UpdatedAt,
			Email:     user.Email,
		}

		w.WriteHeader(201)
		data, _ := json.Marshal(userToReturn)
		w.Write(data)
	})

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	serverErr := server.ListenAndServe()
	if serverErr != nil {
		fmt.Println("Error starting server: ", serverErr)
	}
}
