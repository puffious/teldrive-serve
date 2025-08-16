package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

var (
	teldriveURL           string
	teldriveToken         string
	teldriveAPIURL        string
	enableUploadPage      bool
	streamBufferSize      int
	logConnectionErrors   bool
	httpClient            *http.Client
	templates             *template.Template
	mediaPlayerAgents     = []string{"VLC", "mpv", "LAVF", "Lavf", "ExoPlayer", "Kodi", "Plex", "IINA"}
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
	IsDir bool
	URL   string
	Leaf  string
	Size  int64
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
	godotenv.Load()
	teldriveURL = os.Getenv("TELDRIVE_URL")
	teldriveToken = os.Getenv("TELDRIVE_TOKEN")
	if teldriveURL == "" || teldriveToken == "" {
		log.Fatal("TELDRIVE_URL and TELDRIVE_TOKEN must be set")
	}

	// Parse the ENABLE_UPLOAD_PAGE environment variable
	enableUploadPageStr := strings.ToLower(strings.TrimSpace(os.Getenv("ENABLE_UPLOAD_PAGE")))
	enableUploadPage = enableUploadPageStr == "true" || enableUploadPageStr == "1" || enableUploadPageStr == "yes"

	// Parse buffer size from environment variable (default 256KB)
	streamBufferSize = 256 * 1024 // Default 256KB
	if bufferSizeStr := os.Getenv("STREAM_BUFFER_SIZE"); bufferSizeStr != "" {
		if size, err := strconv.Atoi(bufferSizeStr); err == nil && size > 0 {
			streamBufferSize = size * 1024 // Convert KB to bytes
		}
	}

	// Parse connection error logging setting
	logConnectionErrorsStr := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_CONNECTION_ERRORS")))
	logConnectionErrors = logConnectionErrorsStr == "true" || logConnectionErrorsStr == "1" || logConnectionErrorsStr == "yes"

	teldriveAPIURL = strings.TrimSuffix(teldriveURL, "/") + "/api"
	
	// Optimize HTTP client for better performance with download managers
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,  // Increased for download managers
		MaxConnsPerHost:       50,  // Limit concurrent connections per host
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    true, // Don't compress file transfers
	}
	httpClient = &http.Client{
		Transport: transport,
		Timeout:   0, // No timeout for file downloads
	}
	
	var err error
	templates, err = template.ParseFiles(
		"templates/index.html",
		"templates/upload.html",
	)
	if err != nil {
		log.Fatalf("Error parsing template: %v", err)
	}
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", browseAndDownloadHandler)
	mux.HandleFunc("/site.webmanifest", staticStubHandler)
	mux.HandleFunc("/favicon.ico", staticStubHandler)

	// Only register upload handler if upload page is enabled
	if enableUploadPage {
		mux.HandleFunc("/upload", uploadHandler)
	}

	port := "8888"
	log.Printf("Teldrive Go Proxy running on http://0.0.0.0:%s", port)
	log.Printf("Proxying for Teldrive instance at: %s", teldriveURL)
	log.Printf("Upload page enabled: %v", enableUploadPage)
	log.Printf("Stream buffer size: %d KB", streamBufferSize/1024)

	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

func browseAndDownloadHandler(w http.ResponseWriter, r *http.Request) {
	cleanPath := strings.Trim(r.URL.Path, "/")
	apiPath := "/" + cleanPath

	if cleanPath != "" {
		parentDir := path.Dir(apiPath)
		itemName := path.Base(apiPath)
		parentItems, err := getTeldriveItems(parentDir)
		if err != nil {
			log.Printf("Error checking parent directory '%s': %v", parentDir, err)
			http.Error(w, "Could not contact Teldrive API", http.StatusBadGateway)
			return
		}
		for _, item := range parentItems {
			if item.Name == itemName && item.Type == "file" {
				streamFile(w, r, item)
				return
			}
		}
	}
	renderDirectory(w, r, apiPath, cleanPath)
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
		itemURL := "/" + path.Join(cleanPath, item.Name)
		if isDir {
			itemURL += "/"
		}
		data.Entries = append(data.Entries, TemplateEntry{
			IsDir: isDir, URL: itemURL, Leaf: item.Name, Size: item.Size,
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

func streamFile(w http.ResponseWriter, r *http.Request, file TeldriveFile) {
	streamURL := fmt.Sprintf("%s/files/%s/%s", teldriveAPIURL, file.ID, url.PathEscape(file.Name))
	req, err := http.NewRequest("GET", streamURL, nil)
	if err != nil {
		log.Printf("Error creating stream request: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	req.AddCookie(&http.Cookie{Name: "access_token", Value: teldriveToken})
	
	// Copy relevant headers from client request for better proxying
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	if ifModifiedSince := r.Header.Get("If-Modified-Since"); ifModifiedSince != "" {
		req.Header.Set("If-Modified-Since", ifModifiedSince)
	}
	if ifNoneMatch := r.Header.Get("If-None-Match"); ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	// Add User-Agent to help upstream server identify the request type
	if userAgent := r.Header.Get("User-Agent"); userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	
	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("Error streaming from Teldrive: %v", err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Create a configurable buffered reader for better streaming performance
	bufferedBody := bufio.NewReaderSize(resp.Body, streamBufferSize)

	// Set up response headers - copy more headers for better caching and performance
	header := w.Header()
	for key, values := range resp.Header {
		lowerKey := strings.ToLower(key)
		if lowerKey == "content-type" || lowerKey == "content-length" ||
			lowerKey == "accept-ranges" || lowerKey == "content-range" ||
			lowerKey == "etag" || lowerKey == "last-modified" ||
			lowerKey == "cache-control" || lowerKey == "expires" {
			for _, value := range values {
				header.Add(key, value)
			}
		}
	}
	
	// Add cache headers if not present to improve performance
	if header.Get("Cache-Control") == "" {
		header.Set("Cache-Control", "public, max-age=3600") // Cache for 1 hour
	}

	// Set content disposition based on user agent
	userAgent := r.Header.Get("User-Agent")
	isMediaPlayer := false
	for _, agent := range mediaPlayerAgents {
		if strings.Contains(userAgent, agent) {
			isMediaPlayer = true
			break
		}
	}
	if isMediaPlayer {
		header.Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, file.Name))
	} else {
		header.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, file.Name))
	}

	w.WriteHeader(resp.StatusCode)

	// Use configurable buffer for better throughput
	copyBufferSize := streamBufferSize / 2 // Use half of stream buffer size for copy buffer
	if copyBufferSize < 32*1024 {
		copyBufferSize = 32 * 1024 // Minimum 32KB
	}
	buf := make([]byte, copyBufferSize)
	_, err = io.CopyBuffer(w, bufferedBody, buf)
	if err != nil {
		// Only log connection errors if explicitly enabled
		if !isConnectionError(err) || logConnectionErrors {
			log.Printf("Error during file streaming: %v", err)
		}
		return
	}
}

func staticStubHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
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
	errStr := err.Error()
	return strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "unexpected EOF") ||
		strings.Contains(errStr, "client disconnected")
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
