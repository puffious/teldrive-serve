package services

import (
	"context"
	"net/http"

	"teldrive-go-proxy/internal/models"
)

// FileService handles file operations with Teldrive API
type FileService interface {
	// GetFileMetadata retrieves metadata for a file by ID
	GetFileMetadata(ctx context.Context, fileID string) (*models.TeldriveFile, error)
	
	// ListDirectory lists files in a directory path
	ListDirectory(ctx context.Context, path string) ([]models.TeldriveFile, error)
}

// DownloadService handles file downloads and streaming
type DownloadService interface {
	// StreamFile streams a file directly to the HTTP response writer
	StreamFile(ctx context.Context, fileID string, w http.ResponseWriter, r *http.Request) error
	
	// IsDownloadAllowed checks if a download can proceed (rate limiting, etc.)
	IsDownloadAllowed(fileID string, clientIP string) error
	
	// TrackDownload tracks an active download
	TrackDownload(fileID string, clientIP string)
	
	// UntrackDownload stops tracking a download
	UntrackDownload(fileID string, clientIP string)
}

// TemplateService handles HTML template rendering
type TemplateService interface {
	// RenderDirectory renders the directory listing page
	RenderDirectory(w http.ResponseWriter, data *models.TemplateData) error
	
	// RenderUploadPage renders the upload page (if enabled)
	RenderUploadPage(w http.ResponseWriter) error
}

// TeldriveClient represents the HTTP client for Teldrive API
type TeldriveClient interface {
	// GetFile retrieves file metadata from Teldrive API
	GetFile(ctx context.Context, fileID string) (*models.TeldriveFile, error)
	
	// ListFiles lists files in a directory from Teldrive API
	ListFiles(ctx context.Context, path string) ([]models.TeldriveFile, error)
	
	// StreamFile streams a file from Teldrive API
	StreamFile(ctx context.Context, fileID, filename string, headers map[string]string) (*http.Response, error)
}