package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/joho/godotenv"
)

var (
	teldriveURL       string
	teldriveToken     string
	teldriveAPIURL    string
	httpClient        *http.Client
	templates         *template.Template
	mediaPlayerAgents = []string{"VLC", "mpv", "LAVF", "Lavf", "ExoPlayer", "Kodi", "Plex", "IINA"}
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
	Entries    []TemplateEntry
	Breadcrumb []TemplateBreadcrumb
}

func init() {
	godotenv.Load()
	teldriveURL = os.Getenv("TELDRIVE_URL")
	teldriveToken = os.Getenv("TELDRIVE_TOKEN")
	if teldriveURL == "" || teldriveToken == "" {
		log.Fatal("TELDRIVE_URL and TELDRIVE_TOKEN must be set")
	}
	teldriveAPIURL = strings.TrimSuffix(teldriveURL, "/") + "/api"
	httpClient = &http.Client{}
	var err error
	templates, err = template.ParseFiles("templates/index.html")
	if err != nil {
		log.Fatalf("Error parsing template: %v", err)
	}
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", browseAndDownloadHandler)
	mux.HandleFunc("/site.webmanifest", staticStubHandler)
	mux.HandleFunc("/favicon.ico", staticStubHandler)

	port := "8888"
	log.Printf("Teldrive Go Proxy running on http://0.0.0.0:%s", port)
	log.Printf("Proxying for Teldrive instance at: %s", teldriveURL)

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
		Entries:    make([]TemplateEntry, 0, len(items)),
		Breadcrumb: buildBreadcrumb(cleanPath),
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

	header := w.Header()
	for key, values := range resp.Header {
		lowerKey := strings.ToLower(key)
		if lowerKey == "content-type" || lowerKey == "content-length" ||
			lowerKey == "accept-ranges" || lowerKey == "content-range" ||
			lowerKey == "etag" || lowerKey == "last-modified" {
			for _, value := range values {
				header.Add(key, value)
			}
		}
	}
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
	io.Copy(w, resp.Body)
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
