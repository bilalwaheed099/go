package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"servers/internal/database"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

type apiConfig struct {
	fileserverHits atomic.Int32
	db             *database.Queries
	platform       string
}

type Chirp struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	UserID    uuid.UUID `json:"user_id"`
	Body      string    `json:"body"`
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
	db, _ := sql.Open("postgres", dbURL)
	dbQueries := database.New(db)
	mux := http.NewServeMux()

	apiCfg := apiConfig{
		db:       dbQueries,
		platform: platform,
	}

	mux.Handle("/app/", http.StripPrefix("/app", apiCfg.middlewareMetricsInc(http.FileServer(http.Dir(".")))))
	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(200)
		w.Write([]byte("OK"))
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
			Body   string    `json:"body"`
			UserID uuid.UUID `json:"user_id"`
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

		chirp, err := apiCfg.db.CreateChirp(r.Context(), database.CreateChirpParams{Body: params.Body, UserID: params.UserID})
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

	mux.HandleFunc("POST /api/users", func(w http.ResponseWriter, r *http.Request) {
		type parameters struct {
			Email string `json:"email"`
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

		// creating the new user in DB
		user, err := apiCfg.db.CreateUser(r.Context(), params.Email)
		if err != nil {
			errorResponse := errorResp{
				Error: fmt.Sprintf("Error in creating the user: %v", err),
			}
			w.WriteHeader(500)
			data, _ := json.Marshal(errorResponse)
			w.Write(data)
			return
		}

		type User struct {
			ID        uuid.UUID `json:"id"`
			CreatedAt time.Time `json:"created_at"`
			UpdatedAt time.Time `json:"updated_at"`
			Email     string    `json:"email"`
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

// postgres connection string "postgres://postgres:bilal123@localhost:5432/chripy"
