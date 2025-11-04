package main

import (
	"chirpy/internal/database"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

type Config struct {
	DBURL    string `json:"db_url"`
	Port     string `json:"port"`
	Platform string `json:platform`
}

func LoadConfig() (*Config, error) {
	err := godotenv.Load()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	cfg := &Config{
		DBURL:    os.Getenv("DB_URL"),
		Port:     os.Getenv("PORT"),
		Platform: os.Getenv("PLATFORM"),
	}

	if cfg.Port == "" {
		cfg.Port = "8080"
	}

	return cfg, nil
}

type apiConfig struct {
	fileserverHits atomic.Int32
	db             *database.Queries
	config         *Config
	sqlDB          *sql.DB
}

type UserResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type UserRequest struct {
	Email string `json:"email"`
}

func (cfg *apiConfig) adminResetHandler(w http.ResponseWriter, r *http.Request) {
	if cfg.config.Platform != "dev" {
		http.Error(w, "Forbidden: This endpoint is only accessible in development environments.", http.StatusForbidden)
		return
	}

	ctx := r.Context()
	tx, err := cfg.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		tx.Rollback()
		http.Error(w, "Failed to delete users: "+err.Error(), http.StatusInternalServerError)
		return
	}

	q := database.New(tx)
	err = q.DeleteAllUsers(ctx)
	if err != nil {
		tx.Rollback()
		http.Error(w, "Failed to delete users: "+err.Error(), http.StatusInternalServerError)
		return
	}

	err = tx.Commit()
	if err != nil {
		http.Error(w, "Failed to commit transaction: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("All users deleted successfully."))
}

func createUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req UserRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadGateway)
		return
	}

	if req.Email == "" || !isValidEmailFormat(req.Email) {
		http.Error(w, "Invalid or missing email address", http.StatusBadRequest)
		return
	}

	now := time.Now()
	newUser := UserResponse{
		ID:        uuid.New().String(), // Generate a unique UUID for the user ID
		Email:     req.Email,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Set the Content-Type header to application/json
	w.Header().Set("Content-Type", "application/json")
	// Set the HTTP status code to 201 Created
	w.WriteHeader(http.StatusCreated)

	// Encode the newUser struct into JSON and write it to the response body
	json.NewEncoder(w).Encode(newUser)
}

func isValidEmailFormat(email string) bool {
	// Check for presence of '@' and at least one '.' after '@'
	atIndex := -1
	for i, r := range email {
		if r == '@' {
			atIndex = i
			break
		}
	}
	if atIndex == -1 || atIndex == 0 || atIndex == len(email)-1 {
		return false // No '@', or '@' at start/end
	}

	dotIndex := -1
	for i := atIndex + 1; i < len(email); i++ {
		if email[i] == '.' {
			dotIndex = i
			break
		}
	}
	// Dot must exist after '@' and not be the last character
	return dotIndex != -1 && dotIndex < len(email)-1 && dotIndex > atIndex
}

func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileserverHits.Add(1)
		next.ServeHTTP(w, r)
	})
}

func (cfg *apiConfig) adminMetricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := fmt.Sprintf(`
		<html>
		<body>
		<h1>Welcome, Chirpy Admin</h1>
		<p>Chirpy has been visited %d times!</p>
		</body>
		</html>`, cfg.fileserverHits.Load())
	w.Write([]byte(html))
}
func (cfg *apiConfig) resetHandler(w http.ResponseWriter, r *http.Request) {

	cfg.fileserverHits.Store(0) // Reset the counter
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "Hits reset to 0")


type chirpRequest struct {
	Body string `json:"body"`
}

func (cfg *apiConfig) validateChirpHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		jsonResponse(w, http.StatusMethodNotAllowed, "Something went wrong")
		return
	}

	var request chirpRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		jsonResponse(w, http.StatusBadRequest, "Something went wrong")
		return
	}

	// Validate chirp length
	if len(request.Body) > 140 {
		jsonResponse(w, http.StatusBadRequest, "Chirp is too long")
		return
	}

	profane := map[string]struct{}{
		"kerfuffle": {},
		"sharbert":  {},
		"fornax":    {},
	}

	// split on a single space so punctuation tokens (e.g., "Sharbert!") are NOT matched
	parts := strings.Split(request.Body, " ")
	for i, tok := range parts {
		if _, bad := profane[strings.ToLower(tok)]; bad {
			parts[i] = "****"
		}
	}
	cleaned := strings.Join(parts, " ")

	jsonResponse(w, http.StatusOK, map[string]string{"body": cleaned})
}

func jsonResponse(w http.ResponseWriter, statusCode int, response interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	jsonResponseBody, err := json.Marshal(response)
	if err != nil {
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}

	w.Write(jsonResponseBody)
}

func main() {
	godotenv.Load()

	dbURL := os.Getenv("DB_URL")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		panic(err)
	}

	dbQueries := database.New(db)

	mux := http.NewServeMux()
	apiCfg := &apiConfig{
		db: dbQueries,
	}

	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	mux.Handle("/app/", apiCfg.middlewareMetricsInc(http.StripPrefix("/app/", http.FileServer(http.Dir(".")))))
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("assets/"))))
	mux.HandleFunc("GET /admin/metrics", apiCfg.adminMetricsHandler)
	mux.HandleFunc("POST /admin/reset", apiConfig.adminResetHandler)
	mux.HandleFunc("/api/validate_chirp", apiCfg.validateChirpHandler)
	mux.HandleFunc("POST /api/users", createUserHandler)

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	err = server.ListenAndServe()
	if err != nil {
		panic(err)
	}
}
