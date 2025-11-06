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

	"chirpy/internal/auth"
	"chirpy/internal/database"

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
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (cfg *apiConfig) handlerLogin(w http.ResponseWriter, r *http.Request) {
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

	if req.Password == "" {
		http.Error(w, "Invalid or missing password", http.StatusBadRequest)
		return
	}
	// Look up the user by email - you'll need a database query for this. Do you have a GetUserByEmail query in your sql/queries/users.sql file?
	user, err := cfg.db.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		http.Error(w, "Incorrect email or password", http.StatusUnauthorized)
		return
	}

	passwordValid, err := auth.CheckPasswordHash(req.Password, user.HashedPassword)
	if err != nil || passwordValid == false {
		http.Error(w, "Incorrect email or password", http.StatusUnauthorized)
		return
	}

	response := UserResponse{
		ID:        user.ID.String(),
		Email:     user.Email,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
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

func (cfg *apiConfig) createUserHandler(w http.ResponseWriter, r *http.Request) {
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

	if req.Password == "" {
		http.Error(w, "Invalid or missing password", http.StatusBadRequest)
		return
	}
	// Generate UUID

	userID := uuid.New()
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		http.Error(w, "Error validating password", http.StatusBadRequest)
		return
	}

	// Actually save to database!
	user, err := cfg.db.CreateUser(r.Context(), database.CreateUserParams{
		ID:             userID,
		Email:          req.Email,
		HashedPassword: hash,
	})
	if err != nil {
		fmt.Println("Error creating user:", err)
		http.Error(w, "Failed to create user", http.StatusInternalServerError)
		return
	}

	response := UserResponse{
		ID:        user.ID.String(),
		Email:     user.Email,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
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
}

type chirpRequest struct {
	Body   string    `json:"body"`
	UserID uuid.UUID `json:"user_id"`
}

type chirpResponse struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Body      string    `json:"body"`
	UserID    uuid.UUID `json:"user_id"`
}

func (cfg *apiConfig) handlerChirpsList(w http.ResponseWriter, r *http.Request) {
	chirps, err := cfg.db.GetChirps(r.Context())
	if err != nil {
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}

	// Map DB rows → response DTOs (same structure as POST, but array)
	resp := make([]chirpResponse, 0, len(chirps))
	for _, c := range chirps {
		resp = append(resp, chirpResponse{
			ID:        c.ID,
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
			Body:      c.Body,
			UserID:    c.UserID,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp) // [] on empty, not null
}

func (cfg *apiConfig) handlerGetChirp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		jsonResponse(w, http.StatusMethodNotAllowed, "Http method must be GET")
		return
	}

	chirpID, _ := uuid.Parse(r.PathValue("chirpID"))

	chirp, err := cfg.db.GetChirp(r.Context(), chirpID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		jsonResponse(w, http.StatusNotFound, "Chirp was not found.")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	// Set the HTTP status code to 201 Created
	w.WriteHeader(http.StatusOK)
	response := chirpResponse{
		ID:        chirp.ID,
		CreatedAt: chirp.CreatedAt,
		UpdatedAt: chirp.UpdatedAt,
		Body:      chirp.Body,
		UserID:    chirp.UserID,
	}

	json.NewEncoder(w).Encode(response)
}

func (cfg *apiConfig) handlerChirpsCreate(w http.ResponseWriter, r *http.Request) {
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

	chirpID := uuid.New()

	chirp, err := cfg.db.CreateChirp(r.Context(), database.CreateChirpParams{
		ID:     chirpID,
		Body:   cleaned,
		UserID: request.UserID,
	})
	if err != nil {
		// Log the actual error to see what's wrong
		fmt.Println("Error creating chirp:", err)
		jsonResponse(w, http.StatusInternalServerError, "Something went wrong")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	response := chirpResponse{
		ID:        chirp.ID,
		CreatedAt: chirp.CreatedAt,
		UpdatedAt: chirp.UpdatedAt,
		Body:      chirp.Body,
		UserID:    chirp.UserID,
	}

	json.NewEncoder(w).Encode(response)
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

	cfg, err := LoadConfig()
	if err != nil {
		panic(err)
	}

	dbURL := os.Getenv("DB_URL")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		panic(err)
	}

	dbQueries := database.New(db)

	mux := http.NewServeMux()
	apiCfg := &apiConfig{
		db:     dbQueries,
		config: cfg,
		sqlDB:  db,
	}

	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	mux.Handle("/app/", apiCfg.middlewareMetricsInc(http.StripPrefix("/app/", http.FileServer(http.Dir(".")))))
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("assets/"))))
	mux.HandleFunc("GET /admin/metrics", apiCfg.adminMetricsHandler)
	mux.HandleFunc("POST /admin/reset", apiCfg.adminResetHandler)
	mux.HandleFunc("POST /api/chirps", apiCfg.handlerChirpsCreate)
	mux.HandleFunc("GET /api/chirps/{chirpID}", apiCfg.handlerGetChirp)
	mux.HandleFunc("GET /api/chirps", apiCfg.handlerChirpsList)
	mux.HandleFunc("POST /api/users", apiCfg.createUserHandler)
	mux.HandleFunc("POST /api/login", apiCfg.handlerLogin)

	server := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	err = server.ListenAndServe()
	if err != nil {
		panic(err)
	}
}
