package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/joho/godotenv"
)

var (
	teldriveURL      string
	teldriveToken    string
	teldriveAPIURL   string
	enableUploadPage bool
	httpClient       *http.Client
	templates        *template.Template

	// Rate limiting and connection management
	activeDownloads   sync.Map
	downloadSemaphore chan struct{}

	// Metrics and observability
	serverStartTime   time.Time
	totalRequests     int64
	totalDownloads    int64
	totalErrors       int64
	activeConnections int64

	// File ID validation
	fileIDRegex = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
)

type TeldriveFile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}
type TeldriveListResponse struct {
	Items []TeldriveFile `json:"items"`
}
type TemplateEntry struct {
	IsDir  bool
	URL    string
	Leaf   string
	Size   int64
	FileID string // Add file ID for direct download links
}
type TemplateBreadcrumb struct {
	Link string
	Text string
}
type TemplateData struct {
	Entries          []TemplateEntry
	Breadcrumb       []TemplateBreadcrumb
	EnableUploadPage bool
}

func init() {
	serverStartTime = time.Now()

	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: .env file not found: %v", err)
	}

	// Validate required environment variables
	if err := validateEnvironment(); err != nil {
		log.Fatal(err)
	}

	// Parse the ENABLE_UPLOAD_PAGE environment variable
	enableUploadPageStr := strings.ToLower(strings.TrimSpace(os.Getenv("ENABLE_UPLOAD_PAGE")))
	enableUploadPage = enableUploadPageStr == "true" || enableUploadPageStr == "1" || enableUploadPageStr == "yes"

	teldriveAPIURL = strings.TrimSuffix(teldriveURL, "/") + "/api"

	// Initialize rate limiting - configurable via environment
	maxConcurrentDownloads := getEnvInt("MAX_CONCURRENT_DOWNLOADS", 20)
	downloadSemaphore = make(chan struct{}, maxConcurrentDownloads)

	// OPTIMIZED HTTP client for reliability and performance under load
	setupHTTPClient()

	// Load templates with better error handling
	if err := loadTemplates(); err != nil {
		log.Fatalf("Error loading templates: %v", err)
	}

	log.Printf("Server initialized successfully in %v", time.Since(serverStartTime))
}

func validateEnvironment() error {
	teldriveURL = os.Getenv("TELDRIVE_URL")
	teldriveToken = os.Getenv("TELDRIVE_TOKEN")

	if teldriveURL == "" {
		return fmt.Errorf("TELDRIVE_URL is required but not set")
	}
	if teldriveToken == "" {
		return fmt.Errorf("TELDRIVE_TOKEN is required but not set")
	}

	// Validate URL format
	if _, err := url.Parse(teldriveURL); err != nil {
		return fmt.Errorf("TELDRIVE_URL is not a valid URL: %v", err)
	}

	// Test connectivity to Teldrive (optional but recommended)
	if os.Getenv("SKIP_CONNECTIVITY_CHECK") != "true" {
		if err := testTeldriveConnectivity(); err != nil {
			log.Printf("Warning: Teldrive connectivity test failed: %v", err)
		}
	}

	return nil
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

func setupHTTPClient() {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second, // Increased for reliability
			KeepAlive: 60 * time.Second, // Longer keep-alive
		}).DialContext,
		MaxIdleConns:          100, // Reduced to prevent connection hoarding
		MaxIdleConnsPerHost:   10,  // Reduced per-host to spread load
		IdleConnTimeout:       120 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second, // Timeout for slow responses
		DisableCompression:    true,             // No compression for files
		DisableKeepAlives:     false,            // Keep connections alive for reuse
		// Prevent connection reuse issues under high load
		MaxConnsPerHost: 20,
	}
	httpClient = &http.Client{
		Transport: transport,
		Timeout:   0, // No timeout for downloads
	}
}

func loadTemplates() error {
	var err error
	templates, err = template.ParseFiles(
		"templates/index.html",
		"templates/upload.html",
	)
	return err
}

func testTeldriveConnectivity() error {
	testURL := strings.TrimSuffix(teldriveURL, "/") + "/api/version"
	req, err := http.NewRequest("GET", testURL, nil)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("received status %d", resp.StatusCode)
	}

	return nil
}

// Middleware to track metrics
func withMetrics(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&totalRequests, 1)
		atomic.AddInt64(&activeConnections, 1)
		defer atomic.AddInt64(&activeConnections, -1)

		start := time.Now()

		// Create a response writer wrapper to capture status code
		wrapper := &statusResponseWriter{ResponseWriter: w, statusCode: 200}

		handler(wrapper, r)

		duration := time.Since(start)
		log.Printf("REQUEST: %s %s - Status: %d - Duration: %v - Client: %s",
			r.Method, r.URL.Path, wrapper.statusCode, duration, getClientIP(r))

		if wrapper.statusCode >= 400 {
			atomic.AddInt64(&totalErrors, 1)
		}
	}
}

type statusResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *statusResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":     "healthy",
		"timestamp":  time.Now().UTC(),
		"uptime":     time.Since(serverStartTime).String(),
		"goroutines": runtime.NumGoroutine(),
	}

	// Test Teldrive connectivity
	if err := testTeldriveConnectivity(); err != nil {
		health["status"] = "degraded"
		health["teldrive_error"] = err.Error()
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

func metricsHandler(w http.ResponseWriter, r *http.Request) {
	metrics := map[string]interface{}{
		"total_requests":      atomic.LoadInt64(&totalRequests),
		"total_downloads":     atomic.LoadInt64(&totalDownloads),
		"total_errors":        atomic.LoadInt64(&totalErrors),
		"active_connections":  atomic.LoadInt64(&activeConnections),
		"active_downloads":    getActiveDownloadCount(),
		"goroutines":          runtime.NumGoroutine(),
		"uptime_seconds":      int64(time.Since(serverStartTime).Seconds()),
		"download_queue_size": len(downloadSemaphore),
		"download_queue_cap":  cap(downloadSemaphore),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

func statsHandler(w http.ResponseWriter, r *http.Request) {
	stats := map[string]interface{}{
		"server_info": map[string]interface{}{
			"version":    "1.0.0",
			"go_version": runtime.Version(),
			"start_time": serverStartTime,
			"uptime":     time.Since(serverStartTime).String(),
		},
		"performance": map[string]interface{}{
			"total_requests":     atomic.LoadInt64(&totalRequests),
			"total_downloads":    atomic.LoadInt64(&totalDownloads),
			"total_errors":       atomic.LoadInt64(&totalErrors),
			"active_connections": atomic.LoadInt64(&activeConnections),
			"goroutines":         runtime.NumGoroutine(),
		},
		"configuration": map[string]interface{}{
			"max_concurrent_downloads": cap(downloadSemaphore),
			"upload_page_enabled":      enableUploadPage,
			"teldrive_url":             teldriveURL,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func getActiveDownloadCount() int {
	count := 0
	activeDownloads.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}

func setupGracefulShutdown(server *http.Server) {
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		log.Println("Shutdown signal received, starting graceful shutdown...")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Server forced to shutdown: %v", err)
		}

		log.Println("Server exited")
	}()
}

func main() {
	mux := http.NewServeMux()

	// Core endpoints
	mux.HandleFunc("/", withMetrics(browseAndDownloadHandler))
	mux.HandleFunc("/dl/", withMetrics(directDownloadHandler))

	// Health and monitoring endpoints
	mux.HandleFunc("/health", healthCheckHandler)
	mux.HandleFunc("/metrics", metricsHandler)
	mux.HandleFunc("/stats", statsHandler)

	// Static endpoints
	mux.HandleFunc("/site.webmanifest", staticStubHandler)
	mux.HandleFunc("/favicon.ico", staticStubHandler)

	// Only register upload handler if upload page is enabled
	if enableUploadPage {
		mux.HandleFunc("/upload", withMetrics(uploadHandler))
	}

	port := getEnvInt("PORT", 8888)
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // No write timeout for downloads
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("Teldrive Go Proxy running on http://0.0.0.0:%d", port)
	log.Printf("Proxying for Teldrive instance at: %s", teldriveURL)
	log.Printf("Upload page enabled: %v", enableUploadPage)
	log.Printf("Max concurrent downloads: %d", cap(downloadSemaphore))
	log.Printf("Health check available at: /health")
	log.Printf("Metrics available at: /metrics")
	log.Printf("ENHANCED for reliability - resumable downloads, rate limiting, retry logic")

	// Setup graceful shutdown
	setupGracefulShutdown(server)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed to start: %v", err)
	}
}

func browseAndDownloadHandler(w http.ResponseWriter, r *http.Request) {
	cleanPath := strings.Trim(r.URL.Path, "/")
	apiPath := "/" + cleanPath

	// Only handle directory browsing - all file downloads go through /dl/<fileid>
	renderDirectory(w, r, apiPath, cleanPath)
}

// directDownloadHandler handles direct downloads using file ID in format /dl/<fileid>
func directDownloadHandler(w http.ResponseWriter, r *http.Request) {
	// Wrap the response writer to track headers
	wrapper := &responseWriterWrapper{ResponseWriter: w}
	w = wrapper

	// Rate limiting - acquire semaphore
	select {
	case downloadSemaphore <- struct{}{}:
		defer func() { <-downloadSemaphore }() // Release when done
	case <-time.After(30 * time.Second):
		log.Printf("Download queue full, rejecting request for %s", r.URL.Path)
		http.Error(w, "Server busy, please try again later", http.StatusServiceUnavailable)
		return
	}

	// Extract file ID from URL path /dl/<fileid>
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 2 || pathParts[1] == "" {
		http.Error(w, "Invalid download URL format. Use /dl/<fileid>", http.StatusBadRequest)
		return
	}

	fileID := pathParts[1]
	clientIP := getClientIP(r)

	// Validate file ID format for security
	if !fileIDRegex.MatchString(fileID) {
		log.Printf("INVALID FILE ID: %s from client %s", fileID, clientIP)
		http.Error(w, "Invalid file ID format", http.StatusBadRequest)
		return
	}

	log.Printf("DOWNLOAD START: FileID=%s, Client=%s", fileID, clientIP)

	// Check if this file is already being downloaded too many times
	if countActiveDownloads(fileID) > 5 {
		log.Printf("Too many concurrent downloads for file %s", fileID)
		http.Error(w, "Too many concurrent downloads for this file, please try again later", http.StatusTooManyRequests)
		return
	}

	// Track this download
	downloadKey := fmt.Sprintf("%s:%s", clientIP, fileID)
	activeDownloads.Store(downloadKey, time.Now())
	defer activeDownloads.Delete(downloadKey)

	// Get file metadata first to get the actual filename and correct download URL
	metadata, err := getTeldriveFileMetadata(fileID)
	if err != nil {
		log.Printf("Failed to get metadata for file %s: %v", fileID, err)
		http.Error(w, "File not found or inaccessible", http.StatusNotFound)
		return
	}

	log.Printf("METADATA: File=%s, Size=%d bytes", metadata.Name, metadata.Size)

	// Use the EXACT API endpoint from specification: GET /files/{id}/{name}
	// According to api.yaml, this is the correct streaming/download endpoint
	streamURL := fmt.Sprintf("%s/files/%s/%s", teldriveAPIURL, url.PathEscape(fileID), url.PathEscape(metadata.Name))

	// Add download=1 query parameter for download mode (as per API spec)
	downloadURL := streamURL + "?download=1"

	log.Printf("ATTEMPTING DOWNLOAD: %s", downloadURL)

	if success := tryDownload(w, r, downloadURL, fileID, metadata.Name, true); success {
		atomic.AddInt64(&totalDownloads, 1)
		return // Success!
	}

	// If download=1 fails, try streaming mode (download=0 or omitted)
	log.Printf("Download mode failed, trying stream mode: %s", streamURL)
	if success := tryDownload(w, r, streamURL, fileID, metadata.Name, false); success {
		atomic.AddInt64(&totalDownloads, 1)
		return // Success!
	}

	// Both attempts failed
	log.Printf("All download attempts failed for file %s", fileID)
	if !headersSent(w) {
		http.Error(w, "File temporarily unavailable, please try again", http.StatusBadGateway)
	}
}

func tryDownload(w http.ResponseWriter, r *http.Request, streamURL, fileID, filename string, setHeaders bool) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", streamURL, nil)
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return false
	}

	// Set authentication according to API spec
	// The /files/{id}/{name} endpoint supports both BearerAuth and ApiKeyAuth (cookie)
	req.Header.Set("Authorization", "Bearer "+teldriveToken)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: teldriveToken})

	// Handle range requests for resumable downloads (as per API spec)
	rangeHeader := r.Header.Get("Range")
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
		log.Printf("RANGE REQUEST: %s for file %s", rangeHeader, fileID)
	}

	// Additional headers for better compatibility
	req.Header.Set("User-Agent", "VadaPav-Server/1.0")
	req.Header.Set("Accept", "*/*")

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("HTTP request failed: %v", err)
		return false
	}
	defer resp.Body.Close()

	// Check for successful response
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("Bad response status: %d %s", resp.StatusCode, resp.Status)
		return false
	}

	// Set response headers (only on first successful attempt)
	if setHeaders && !headersSent(w) {
		setDownloadHeaders(w, resp, filename)
	}

	// Start streaming with improved error handling
	return streamWithRetry(w, resp.Body, fileID)
}

func setDownloadHeaders(w http.ResponseWriter, resp *http.Response, filename string) {
	header := w.Header()

	// Copy headers exactly as specified in the API spec for /files/{id}/{name}
	// According to api.yaml, these headers are required/expected:
	for key, values := range resp.Header {
		lowerKey := strings.ToLower(key)
		switch lowerKey {
		case "accept-ranges":
			for _, value := range values {
				header.Add("Accept-Ranges", value)
			}
		case "content-length":
			for _, value := range values {
				header.Add("Content-Length", value)
			}
		case "content-disposition":
			for _, value := range values {
				header.Add("Content-Disposition", value)
			}
		case "content-range":
			for _, value := range values {
				header.Add("Content-Range", value)
			}
		case "etag":
			for _, value := range values {
				header.Add("Etag", value)
			}
		case "last-modified":
			for _, value := range values {
				header.Add("Last-Modified", value)
			}
		case "content-type":
			for _, value := range values {
				header.Add("Content-Type", value)
			}
		}
	}

	// Ensure required headers are set (as per API specification)
	if header.Get("Accept-Ranges") == "" {
		header.Set("Accept-Ranges", "bytes")
	}

	// If Content-Disposition not set by upstream, set it with actual filename
	if header.Get("Content-Disposition") == "" {
		header.Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	}

	// Add caching headers for better performance
	header.Set("Cache-Control", "public, max-age=31536000") // 1 year cache

	w.WriteHeader(resp.StatusCode)
}

func streamWithRetry(w http.ResponseWriter, body io.Reader, fileID string) bool {
	// Use a buffered copy for better performance under load
	buffer := make([]byte, 64*1024) // 64KB buffer - good balance

	for {
		n, err := body.Read(buffer)
		if n > 0 {
			written, writeErr := w.Write(buffer[:n])
			if writeErr != nil {
				if !isConnectionError(writeErr) {
					log.Printf("Write error for file %s: %v", fileID, writeErr)
				}
				return false
			}
			if written != n {
				log.Printf("Partial write for file %s: %d/%d bytes", fileID, written, n)
				return false
			}

			// Flush data to client
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}

		if err != nil {
			if err == io.EOF {
				log.Printf("DOWNLOAD SUCCESS: File %s completed", fileID)
				return true
			}
			if !isConnectionError(err) {
				log.Printf("Read error for file %s: %v", fileID, err)
			}
			return false
		}
	}
}

// Helper functions
func getClientIP(r *http.Request) string {
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.Header.Get("X-Real-IP")
	}
	if ip == "" {
		ip = r.RemoteAddr
	}
	return strings.Split(ip, ",")[0]
}

func countActiveDownloads(fileID string) int {
	count := 0
	activeDownloads.Range(func(key, value interface{}) bool {
		if strings.Contains(key.(string), fileID) {
			count++
		}
		return true
	})
	return count
}

// ResponseWriterWrapper to track if headers have been sent
type responseWriterWrapper struct {
	http.ResponseWriter
	headersSent bool
}

func (w *responseWriterWrapper) WriteHeader(code int) {
	if !w.headersSent {
		w.ResponseWriter.WriteHeader(code)
		w.headersSent = true
	}
}

func (w *responseWriterWrapper) Write(data []byte) (int, error) {
	if !w.headersSent {
		w.headersSent = true
	}
	return w.ResponseWriter.Write(data)
}

func headersSent(w http.ResponseWriter) bool {
	if wrapper, ok := w.(*responseWriterWrapper); ok {
		return wrapper.headersSent
	}
	return false
}

func staticStubHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func renderDirectory(w http.ResponseWriter, r *http.Request, apiPath, cleanPath string) {
	items, err := getTeldriveItems(apiPath)
	if err != nil {
		http.Error(w, "Could not fetch directory listing", http.StatusBadGateway)
		return
	}
	data := TemplateData{
		Entries:          make([]TemplateEntry, 0, len(items)),
		Breadcrumb:       buildBreadcrumb(cleanPath),
		EnableUploadPage: enableUploadPage,
	}
	for _, item := range items {
		isDir := item.Type == "folder"
		var itemURL string
		if isDir {
			itemURL = "/" + path.Join(cleanPath, item.Name) + "/"
		}
		data.Entries = append(data.Entries, TemplateEntry{
			IsDir:  isDir,
			URL:    itemURL, // Only used for directories now
			Leaf:   item.Name,
			Size:   item.Size,
			FileID: item.ID,
		})
	}
	sort.SliceStable(data.Entries, func(i, j int) bool {
		if data.Entries[i].IsDir != data.Entries[j].IsDir {
			return data.Entries[i].IsDir
		}
		return strings.ToLower(data.Entries[i].Leaf) < strings.ToLower(data.Entries[j].Leaf)
	})
	if err := templates.Execute(w, data); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func getTeldriveItems(apiPath string) ([]TeldriveFile, error) {
	listURL := fmt.Sprintf("%s/files?path=%s&limit=1000", teldriveAPIURL, url.QueryEscape(apiPath))
	req, err := http.NewRequest("GET", listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("could not create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+teldriveToken)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("Teldrive API Error: GET %s returned status %s", listURL, resp.Status)
		if resp.StatusCode == http.StatusNotFound {
			return []TeldriveFile{}, nil
		}
		return nil, fmt.Errorf("API returned non-200 status: %s", resp.Status)
	}
	var listResponse TeldriveListResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResponse); err != nil {
		return nil, fmt.Errorf("could not decode API response: %w", err)
	}
	return listResponse.Items, nil
}

func getTeldriveFileMetadata(fileID string) (*TeldriveFile, error) {
	// Use the exact API endpoint from the spec: GET /files/{id}
	metadataURL := fmt.Sprintf("%s/files/%s", teldriveAPIURL, url.PathEscape(fileID))
	req, err := http.NewRequest("GET", metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("could not create metadata request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+teldriveToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("metadata API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Metadata API Error: GET %s returned status %s", metadataURL, resp.Status)
		return nil, fmt.Errorf("metadata API returned status: %s", resp.Status)
	}

	var fileInfo TeldriveFile
	if err := json.NewDecoder(resp.Body).Decode(&fileInfo); err != nil {
		return nil, fmt.Errorf("could not decode metadata response: %w", err)
	}

	return &fileInfo, nil
}

func buildBreadcrumb(cleanPath string) []TemplateBreadcrumb {
	if cleanPath == "" {
		return nil
	}
	parts := strings.Split(cleanPath, "/")
	breadcrumbs := make([]TemplateBreadcrumb, 0, len(parts)+1)
	breadcrumbs = append(breadcrumbs, TemplateBreadcrumb{Link: "/", Text: "root"})
	currentPath := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		currentPath = path.Join(currentPath, part)
		breadcrumbs = append(breadcrumbs, TemplateBreadcrumb{Link: "/" + currentPath, Text: part})
	}
	return breadcrumbs
}

// isConnectionError checks if the error is a connection-related error
// that's normal when clients (like aria2c) disconnect early
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "unexpected EOF") ||
		strings.Contains(errStr, "client disconnected") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no route to host") ||
		strings.Contains(errStr, "network is unreachable") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "context canceled") ||
		strings.Contains(errStr, "context deadline exceeded")
}

// Background cleanup of stale downloads
func init() {
	go func() {
		ticker := time.NewTicker(60 * time.Second) // Cleanup every minute
		defer ticker.Stop()

		for range ticker.C {
			cleanupStaleDownloads()
		}
	}()
}

func cleanupStaleDownloads() {
	staleThreshold := 10 * time.Minute
	now := time.Now()

	activeDownloads.Range(func(key, value interface{}) bool {
		if startTime, ok := value.(time.Time); ok {
			if now.Sub(startTime) > staleThreshold {
				activeDownloads.Delete(key)
				log.Printf("Cleaned up stale download: %s", key)
			}
		}
		return true
	})
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	// Check if upload page is enabled
	if !enableUploadPage {
		http.Error(w, "Upload functionality is disabled", http.StatusNotFound)
		return
	}

	if r.Method == "GET" {
		templates.ExecuteTemplate(w, "upload.html", nil)
		return
	}

	if r.Method == "POST" {
		// Create directories if they don't exist
		os.MkdirAll("/stuff/torrents", 0755)
		os.MkdirAll("/stuff/magnets", 0755)
		os.MkdirAll("/stuff/ddl", 0755)

		// Handle magnet URL
		if magnet := r.FormValue("magnet"); magnet != "" {
			appendToFile("/stuff/magnets.txt", magnet+"\n")
		}

		// Handle torrent file
		if file, header, err := r.FormFile("torrent"); err == nil {
			defer file.Close()
			if filepath.Ext(header.Filename) == ".torrent" {
				saveFile("/stuff/torrents/"+header.Filename, file)
			}
		}

		// Handle DDL
		if ddl := r.FormValue("ddl"); ddl != "" {
			category := r.FormValue("category")
			imdb := r.FormValue("imdb")
			entry := ddl
			if imdb != "" {
				entry += " [" + imdb + "]"
			}
			entry += " [" + category + "]\n"
			appendToFile("/stuff/ddl.txt", entry)
		}

		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func appendToFile(filename, content string) error {
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err
}

func saveFile(filepath string, file io.Reader) error {
	data, err := io.ReadAll(file)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath, data, 0644)
}
