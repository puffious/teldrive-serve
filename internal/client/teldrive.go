package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"teldrive-go-proxy/internal/config"
	"teldrive-go-proxy/internal/models"
	"teldrive-go-proxy/pkg/logger"
)

// TeldriveClient implements the TeldriveClient interface
type TeldriveClient struct {
	config     *config.Config
	httpClient *http.Client
	logger     *logger.Logger
}

// New creates a new TeldriveClient
func New(cfg *config.Config, httpClient *http.Client, log *logger.Logger) *TeldriveClient {
	return &TeldriveClient{
		config:     cfg,
		httpClient: httpClient,
		logger:     log,
	}
}

// GetFile retrieves file metadata from Teldrive API
// Extracted from getTeldriveFileMetadata() in main.go (lines 665-691)
func (c *TeldriveClient) GetFile(ctx context.Context, fileID string) (*models.TeldriveFile, error) {
	// Use the exact API endpoint from the spec: GET /files/{id}
	metadataURL := fmt.Sprintf("%s/files/%s", c.config.Teldrive.APIURL, url.PathEscape(fileID))
	
	req, err := http.NewRequestWithContext(ctx, "GET", metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("could not create metadata request: %w", err)
	}
	
	// Set authentication according to API spec
	req.Header.Set("Authorization", "Bearer "+c.config.Teldrive.Token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("metadata API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.logger.Printf("Metadata API Error: GET %s returned status %s", metadataURL, resp.Status)
		return nil, fmt.Errorf("metadata API returned status: %s", resp.Status)
	}

	var fileInfo models.TeldriveFile
	if err := json.NewDecoder(resp.Body).Decode(&fileInfo); err != nil {
		return nil, fmt.Errorf("could not decode metadata response: %w", err)
	}

	return &fileInfo, nil
}

// ListFiles lists files in a directory from Teldrive API
// Extracted from getTeldriveItems() in main.go (lines 639-663)
func (c *TeldriveClient) ListFiles(ctx context.Context, path string) ([]models.TeldriveFile, error) {
	listURL := fmt.Sprintf("%s/files?path=%s&limit=1000", c.config.Teldrive.APIURL, url.QueryEscape(path))
	
	req, err := http.NewRequestWithContext(ctx, "GET", listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("could not create request: %w", err)
	}
	
	req.Header.Set("Authorization", "Bearer "+c.config.Teldrive.Token)
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		c.logger.Printf("Teldrive API Error: GET %s returned status %s", listURL, resp.Status)
		if resp.StatusCode == http.StatusNotFound {
			return []models.TeldriveFile{}, nil
		}
		return nil, fmt.Errorf("API returned non-200 status: %s", resp.Status)
	}
	
	var listResponse models.TeldriveListResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResponse); err != nil {
		return nil, fmt.Errorf("could not decode API response: %w", err)
	}
	
	return listResponse.Items, nil
}

// StreamFile streams a file from Teldrive API
// This sets up the request but returns the response for the caller to handle streaming
func (c *TeldriveClient) StreamFile(ctx context.Context, fileID, filename string, headers map[string]string) (*http.Response, error) {
	// Use the EXACT API endpoint from specification: GET /files/{id}/{name}
	streamURL := fmt.Sprintf("%s/files/%s/%s", c.config.Teldrive.APIURL, url.PathEscape(fileID), url.PathEscape(filename))

	req, err := http.NewRequestWithContext(ctx, "GET", streamURL, nil)
	if err != nil {
		return nil, fmt.Errorf("could not create stream request: %w", err)
	}

	// Set authentication according to API spec
	req.Header.Set("Authorization", "Bearer "+c.config.Teldrive.Token)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: c.config.Teldrive.Token})

	// Add any additional headers (like Range for resumable downloads)
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	// Additional headers for better compatibility and speed
	req.Header.Set("User-Agent", "VadaPav-Server/1.0")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Encoding", "identity") // Prevent compression
	req.Header.Set("Connection", "keep-alive")    // Encourage connection reuse

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stream request failed: %w", err)
	}

	// Check for successful response
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("bad response status: %d %s", resp.StatusCode, resp.Status)
	}

	return resp, nil
}