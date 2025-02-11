package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
)

type apiConfig struct {
	fileserverHits atomic.Int32
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
	mux := http.NewServeMux()

	apiCfg := apiConfig{}

	mux.Handle("/app/", http.StripPrefix("/app", apiCfg.middlewareMetricsInc(http.FileServer(http.Dir(".")))))
	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	})

	mux.HandleFunc("GET /admin/metrics", apiCfg.metricsHandler)
	mux.HandleFunc("POST /admin/reset", apiCfg.resetHandler)
	mux.HandleFunc("POST /api/validate_chirp", func(w http.ResponseWriter, r *http.Request) {
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

		w.WriteHeader(200)
		cleanedResponse := cleanedResp{
			CleanedBody: clean(params.Body),
		}

		data, _ := json.Marshal(cleanedResponse)
		w.Write(data)
	})

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	err := server.ListenAndServe()
	if err != nil {
		fmt.Println("Error starting server: ", err)
	}
}
