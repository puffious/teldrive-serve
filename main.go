package main

import (
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
	"strings"
	"time"

	"github.com/joho/godotenv"
)

var (
	teldriveURL         string
	teldriveToken       string
	teldriveAPIURL      string
	enableUploadPage    bool
	logConnectionErrors bool
	httpClient          *http.Client
	templates           *template.Template
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
	godotenv.Load()
	teldriveURL = os.Getenv("TELDRIVE_URL")
	teldriveToken = os.Getenv("TELDRIVE_TOKEN")
	if teldriveURL == "" || teldriveToken == "" {
		log.Fatal("TELDRIVE_URL and TELDRIVE_TOKEN must be set")
	}

	// Parse the ENABLE_UPLOAD_PAGE environment variable
	enableUploadPageStr := strings.ToLower(strings.TrimSpace(os.Getenv("ENABLE_UPLOAD_PAGE")))
	enableUploadPage = enableUploadPageStr == "true" || enableUploadPageStr == "1" || enableUploadPageStr == "yes"

	// Parse connection error logging setting
	logConnectionErrorsStr := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_CONNECTION_ERRORS")))
	logConnectionErrors = logConnectionErrorsStr == "true" || logConnectionErrorsStr == "1" || logConnectionErrorsStr == "yes"

	teldriveAPIURL = strings.TrimSuffix(teldriveURL, "/") + "/api"

	// SIMPLIFIED HTTP client for maximum performance
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second, // Reduced timeout
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        200, // Increased for high load
		MaxIdleConnsPerHost: 50,  // Increased for high load
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableCompression:  true, // No compression for files
	}
	httpClient = &http.Client{
		Transport: transport,
		Timeout:   0, // No timeout for downloads
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
	mux.HandleFunc("/dl/", directDownloadHandler)
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
	log.Printf("SIMPLIFIED for maximum performance - direct streaming")

	if err := http.ListenAndServe(":"+port, mux); err != nil {
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
	// Extract file ID from URL path /dl/<fileid>
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 2 || pathParts[1] == "" {
		http.Error(w, "Invalid download URL format. Use /dl/<fileid>", http.StatusBadRequest)
		return
	}

	fileID := pathParts[1]

	// Try direct download endpoint - some Teldrive instances support /download
	streamURL := fmt.Sprintf("%s/files/%s/download", teldriveAPIURL, url.PathEscape(fileID))

	req, err := http.NewRequest("GET", streamURL, nil)
	if err != nil {
		log.Printf("Error creating stream request: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	req.AddCookie(&http.Cookie{Name: "access_token", Value: teldriveToken})

	// Copy only essential headers
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("Error streaming from Teldrive: %v", err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy essential response headers only
	header := w.Header()
	for key, values := range resp.Header {
		lowerKey := strings.ToLower(key)
		if lowerKey == "content-type" || lowerKey == "content-length" ||
			lowerKey == "accept-ranges" || lowerKey == "content-range" {
			for _, value := range values {
				header.Add(key, value)
			}
		}
	}

	w.WriteHeader(resp.StatusCode)

	// DIRECT PIPE - NO BUFFERING OVERHEAD
	_, err = io.Copy(w, resp.Body)
	if err != nil && !isConnectionError(err) {
		log.Printf("Error during file streaming: %v", err)
	}
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
