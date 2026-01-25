package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

// Config holds application configuration
type Config struct {
	TeldriveURL    string
	TeldriveToken  string
	TeldriveHash   string
	TeldriveDLURL  string
	DisableLogs    bool
	HTTPPort       string
}

// FileMetadata represents cached file info
type FileMetadata struct {
	Data      map[string]interface{}
	Timestamp time.Time
}

// FileItem represents a file/folder item
type FileItem struct {
	ID   string                 `json:"id"`
	Name string                 `json:"name"`
	Type string                 `json:"type"`
	Size int64                  `json:"size"`
	Data map[string]interface{} `json:"-"`
}

// FilesResponse represents API response
type FilesResponse struct {
	Items []map[string]interface{} `json:"items"`
}

// App holds application state
type App struct {
	config Config
	client *http.Client
	cache  map[string]FileMetadata
	mu     sync.RWMutex
	apiURL string
}

const (
	cacheTTL    = 5 * time.Minute
	chunkSize   = 8 * 1024 * 1024
	readTimeout = 30 * time.Second
)

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

func loadConfig() (Config, error) {
	_ = godotenv.Load()

	config := Config{
		TeldriveURL:   os.Getenv("TELDRIVE_URL"),
		TeldriveToken: os.Getenv("TELDRIVE_TOKEN"),
		TeldriveHash:  os.Getenv("TELDRIVE_HASH"),
		TeldriveDLURL: os.Getenv("TELDRIVE_DL_URL"),
		HTTPPort:      getEnvOrDefault("HTTP_PORT", "8888"),
		DisableLogs:   strings.ToLower(os.Getenv("DISABLE_LOGS")) == "true",
	}

	if config.TeldriveURL == "" || config.TeldriveToken == "" || config.TeldriveHash == "" {
		return config, fmt.Errorf("TELDRIVE_URL, TELDRIVE_TOKEN, and TELDRIVE_HASH must be set in .env file")
	}

	return config, nil
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// log logs a message if logging is enabled
func (app *App) log(message string) {
	if !app.config.DisableLogs {
		fmt.Println(message)
	}
}

// NewApp creates a new application instance
func NewApp(config Config) *App {
	// Create HTTP client with connection pooling
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		MaxConnsPerHost:     100,
		IdleConnTimeout:     600 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 600 * time.Second,
		}).DialContext,
		DisableKeepAlives:     false,
		DisableCompression:    false,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   0, // No timeout, let context handle it
	}

	app := &App{
		config: config,
		client: client,
		cache:  make(map[string]FileMetadata),
		apiURL: strings.TrimRight(config.TeldriveURL, "/") + "/api",
	}

	return app
}

// getTeldriveItems fetches directory listing
func (app *App) getTeldriveItems(path string) ([]map[string]interface{}, int, error) {
	req, _ := http.NewRequest("GET", app.apiURL+"/files", nil)
	req.Header.Set("Authorization", "Bearer "+app.config.TeldriveToken")

	q := req.URL.Query()
	q.Add("path", path)
	q.Add("limit", "1000")
	req.URL.RawQuery = q.Encode()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := app.client.Do(req)
	if err != nil {
		app.log(fmt.Sprintf("Error fetching from Teldrive API (Path: %s): %v", path, err))
		return nil, 502, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return []map[string]interface{}{}, 404, nil
	}

	if resp.StatusCode != 200 {
		app.log(fmt.Sprintf("Unexpected status from Teldrive API: %d", resp.StatusCode))
		return nil, resp.StatusCode, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	// Parse JSON response
	var result FilesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		app.log(fmt.Sprintf("Error decoding response: %v", err))
		return nil, 500, err
	}

	return result.Items, 200, nil
}

// getTeldriveFileByID fetches file metadata by ID
func (app *App) getTeldriveFileByID(fileID string, useCache bool) (map[string]interface{}, error) {
	// Check cache
	if useCache {
		app.mu.RLock()
		if cached, exists := app.cache[fileID]; exists {
			if time.Since(cached.Timestamp) < cacheTTL {
				app.mu.RUnlock()
				return cached.Data, nil
			}
		}
		app.mu.RUnlock()
	}

	req, _ := http.NewRequest("GET", app.apiURL+"/files/"+fileID, nil)
	req.Header.Set("Authorization", "Bearer "+app.config.TeldriveToken)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := app.client.Do(req)
	if err != nil {
		app.log(fmt.Sprintf("Error fetching file by ID from Teldrive API (ID: %s): %v", fileID, err))
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("not found")
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	// Parse JSON response
	fileData := make(map[string]interface{})
	if err := json.NewDecoder(resp.Body).Decode(&fileData); err != nil {
		app.log(fmt.Sprintf("Error decoding file response: %v", err))
		return nil, err
	}

	// Cache the result
	if useCache {
		app.mu.Lock()
		app.cache[fileID] = FileMetadata{
			Data:      fileData,
			Timestamp: time.Now(),
		}
		app.mu.Unlock()
	}

	return fileData, nil
}

// directDownload handles /dl/<file_id> downloads
func (app *App) directDownload(w http.ResponseWriter, r *http.Request) {
	fileID := strings.TrimPrefix(r.URL.Path, "/dl/")

	fileItem, err := app.getTeldriveFileByID(fileID, true)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	fileName, ok := fileItem["name"].(string)
	if !ok {
		http.Error(w, "Invalid file metadata", http.StatusInternalServerError)
		return
	}

	// Construct download URL
	downloadURL := fmt.Sprintf(
		"%s/api/files/%s/%s?hash=%s&download=1",
		strings.TrimRight(app.config.TeldriveDLURL, "/"),
		fileID,
		url.PathEscape(fileName),
		app.config.TeldriveHash,
	)

	// Create request to upstream
	req, _ := http.NewRequest("GET", downloadURL, nil)

	// Forward Range header
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := app.client.Do(req)
	if err != nil {
		app.log(fmt.Sprintf("Error proxying download for %s: %v", fileID, err))
		http.Error(w, "Could not connect to the download server", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Forward response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Override Content-Disposition
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	// Ensure Accept-Ranges header
	if w.Header().Get("Accept-Ranges") == "" {
		w.Header().Set("Accept-Ranges", "bytes")
	}

	w.WriteHeader(resp.StatusCode)

	// Stream response with graceful error handling
	buf := make([]byte, chunkSize)
	_, err = io.CopyBuffer(w, resp.Body, buf)
	if err != nil && err != io.EOF {
		app.log(fmt.Sprintf("Stream error for %s: %v", fileID, err))
	}
}

// browse handles directory browsing
func (app *App) browse(w http.ResponseWriter, r *http.Request) {
	// Block /dl/ routes
	if strings.HasPrefix(r.URL.Path, "/dl/") {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	// Render simple HTML listing
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
	<title>Teldrive File Browser</title>
	<style>
		body { font-family: Arial, sans-serif; margin: 20px; }
		.breadcrumb { margin-bottom: 20px; }
		.breadcrumb a { margin-right: 10px; }
		.entries { list-style: none; padding: 0; }
		.entries li { padding: 10px; border-bottom: 1px solid #ddd; }
		.entries a { text-decoration: none; color: #0066cc; }
		.entries a:hover { text-decoration: underline; }
		.size { color: #666; font-size: 0.9em; float: right; }
	</style>
</head>
<body>
	<h1>File Browser</h1>
	<div class="breadcrumb">
		<a href="/">root</a>
	</div>
	<ul class="entries">
		<li><a href="/">Browse Files</a></li>
	</ul>
</body>
</html>`)
}

// setupRoutes configures HTTP routes
func (app *App) setupRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok"}`)
	})

	mux.HandleFunc("/dl/", app.directDownload)
	mux.HandleFunc("/", app.browse)

	return mux
}

func main() {
	config, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		os.Exit(1)
	}

	app := NewApp(config)
	mux := app.setupRoutes()

	addr := "0.0.0.0:" + config.HTTPPort
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  readTimeout,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  600 * time.Second,
	}

	fmt.Printf("Starting Teldrive proxy on %s\n", addr)
	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
