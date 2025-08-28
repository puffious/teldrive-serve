package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"teldrive-go-proxy/internal/config"
	"teldrive-go-proxy/pkg/errors"
	"teldrive-go-proxy/pkg/logger"
)

// downloadService implements the DownloadService interface
type downloadService struct {
	config            *config.Config
	teldriveClient    TeldriveClient
	fileService       FileService
	logger            *logger.Logger
	downloadSemaphore chan struct{}
	activeDownloads   sync.Map
}

// NewDownloadService creates a new DownloadService
func NewDownloadService(cfg *config.Config, client TeldriveClient, fileService FileService, log *logger.Logger) DownloadService {
	// Create rate limiting semaphore (extracted from main.go lines 92-94)
	downloadSemaphore := make(chan struct{}, cfg.Features.MaxConcurrentDownloads)
	
	service := &downloadService{
		config:            cfg,
		teldriveClient:    client,
		fileService:       fileService,
		logger:            log,
		downloadSemaphore: downloadSemaphore,
	}

	// Start background cleanup (extracted from main.go lines 731-740)
	go service.startCleanupWorker()
	
	return service
}

// StreamFile streams a file directly to the HTTP response writer
// This is the main function extracted from directDownloadHandler in main.go (lines 298-379)
func (s *downloadService) StreamFile(ctx context.Context, fileID string, w http.ResponseWriter, r *http.Request) error {
	clientIP := getClientIP(r)
	
	s.logger.Printf("DOWNLOAD START: FileID=%s, Client=%s", fileID, clientIP)

	// Check if this file is already being downloaded too many times (lines 334-339)
	if s.countActiveDownloads(fileID) > 5 {
		s.logger.Printf("Too many concurrent downloads for file %s", fileID)
		return errors.New("TOO_MANY_REQUESTS", "Too many concurrent downloads for this file, please try again later")
	}

	// Get file metadata first to get the actual filename
	metadata, err := s.fileService.GetFileMetadata(ctx, fileID)
	if err != nil {
		s.logger.Printf("Failed to get metadata for file %s: %v", fileID, err)
		return errors.New("FILE_NOT_FOUND", "File not found or inaccessible")
	}

	s.logger.Printf("METADATA: File=%s, Size=%d bytes", metadata.Name, metadata.Size)

	// Try download with download=1 parameter first, then fallback to streaming
	if success := s.tryDownload(ctx, w, r, fileID, metadata.Name, true); success {
		return nil
	}

	s.logger.Printf("Download mode failed, trying stream mode for file %s", fileID)
	if success := s.tryDownload(ctx, w, r, fileID, metadata.Name, false); success {
		return nil
	}

	s.logger.Printf("All download attempts failed for file %s", fileID)
	return errors.New("DOWNLOAD_FAILED", "File temporarily unavailable, please try again")
}

// IsDownloadAllowed checks if a download can proceed (rate limiting, etc.)
func (s *downloadService) IsDownloadAllowed(fileID string, clientIP string) error {
	// Rate limiting - acquire semaphore (lines 304-312)
	select {
	case s.downloadSemaphore <- struct{}{}:
		// Success - acquired semaphore
		return nil
	case <-time.After(30 * time.Second):
		s.logger.Printf("Download queue full, rejecting request for %s", fileID)
		return errors.New("SERVICE_UNAVAILABLE", "Server busy, please try again later")
	}
}

// TrackDownload tracks an active download
func (s *downloadService) TrackDownload(fileID string, clientIP string) {
	downloadKey := fmt.Sprintf("%s:%s", clientIP, fileID)
	s.activeDownloads.Store(downloadKey, time.Now())
}

// UntrackDownload stops tracking a download
func (s *downloadService) UntrackDownload(fileID string, clientIP string) {
	downloadKey := fmt.Sprintf("%s:%s", clientIP, fileID)
	s.activeDownloads.Delete(downloadKey)
	
	// Release semaphore
	<-s.downloadSemaphore
}

// tryDownload attempts to download with retry logic
// Extracted from tryDownload in main.go (lines 381-402)
func (s *downloadService) tryDownload(ctx context.Context, w http.ResponseWriter, r *http.Request, fileID, filename string, useDownloadMode bool) bool {
	maxRetries := 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s
			backoffTime := time.Duration(1<<uint(attempt-1)) * time.Second
			s.logger.Printf("Retry attempt %d for file %s after %v", attempt+1, fileID, backoffTime)
			time.Sleep(backoffTime)
		}

		if success := s.attemptSingleDownload(ctx, w, r, fileID, filename, useDownloadMode); success {
			return true
		}

		if attempt == maxRetries-1 {
			s.logger.Printf("All %d download attempts failed for file %s", maxRetries, fileID)
		}
	}
	return false
}

// attemptSingleDownload performs a single download attempt
// Extracted from attemptSingleDownload in main.go (lines 404-452)
func (s *downloadService) attemptSingleDownload(ctx context.Context, w http.ResponseWriter, r *http.Request, fileID, filename string, useDownloadMode bool) bool {
	// Use a longer timeout for individual requests
	downloadCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	// Build headers for the request
	headers := make(map[string]string)
	
	// Handle range requests for resumable downloads
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		headers["Range"] = rangeHeader
		s.logger.Printf("RANGE REQUEST: %s for file %s", rangeHeader, fileID)
	}

	// Add download parameter if requested
	streamURL := fileID + "/" + filename
	if useDownloadMode {
		streamURL += "?download=1"
	}

	// Get streaming response from TeldriveClient
	resp, err := s.teldriveClient.StreamFile(downloadCtx, fileID, filename, headers)
	if err != nil {
		s.logger.Printf("Stream request failed: %v", err)
		return false
	}
	defer resp.Body.Close()

	// Set response headers on first successful attempt
	if !s.headersSent(w) {
		s.setDownloadHeaders(w, resp, filename)
	}

	// Start streaming with improved error handling
	return s.streamWithRetry(w, resp.Body, fileID)
}

// setDownloadHeaders sets appropriate download headers
// Extracted from setDownloadHeaders in main.go (lines 454-507)
func (s *downloadService) setDownloadHeaders(w http.ResponseWriter, resp *http.Response, filename string) {
	header := w.Header()

	// Copy headers exactly as specified in the API spec
	for key, values := range resp.Header {
		lowerKey := strings.ToLower(key)
		switch lowerKey {
		case "accept-ranges", "content-length", "content-disposition", 
		     "content-range", "etag", "last-modified", "content-type":
			for _, value := range values {
				header.Add(http.CanonicalHeaderKey(key), value)
			}
		}
	}

	// Ensure required headers are set
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

// streamWithRetry handles the actual file streaming
// Extracted from streamWithRetry in main.go (lines 509-546)
func (s *downloadService) streamWithRetry(w http.ResponseWriter, body io.Reader, fileID string) bool {
	// Use configured buffer size for optimal performance
	bufferSize := s.config.StreamBufferSizeBytes()
	if bufferSize < 1024*128 { // Minimum 128KB
		bufferSize = 1024 * 1024 // Default to 1MB
	}
	
	buffer := make([]byte, bufferSize)

	for {
		n, err := body.Read(buffer)
		if n > 0 {
			written, writeErr := w.Write(buffer[:n])
			if writeErr != nil {
				if !errors.IsConnectionError(writeErr) {
					s.logger.Printf("Write error for file %s: %v", fileID, writeErr)
				}
				return false
			}
			if written != n {
				s.logger.Printf("Partial write for file %s: %d/%d bytes", fileID, written, n)
				return false
			}

			// Flush data to client
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}

		if err != nil {
			if err == io.EOF {
				s.logger.Printf("DOWNLOAD SUCCESS: File %s completed", fileID)
				return true
			}
			if !errors.IsConnectionError(err) {
				s.logger.Printf("Read error for file %s: %v", fileID, err)
			}
			return false
		}
	}
}

// countActiveDownloads counts how many active downloads exist for a file
// Extracted from countActiveDownloads in main.go (lines 560-569)
func (s *downloadService) countActiveDownloads(fileID string) int {
	count := 0
	s.activeDownloads.Range(func(key, value interface{}) bool {
		if strings.Contains(key.(string), fileID) {
			count++
		}
		return true
	})
	return count
}

// headersSent checks if headers have been sent (placeholder for wrapper logic)
func (s *downloadService) headersSent(w http.ResponseWriter) bool {
	// This would need the responseWriterWrapper from main.go
	// For now, return false - headers can be set
	return false
}

// startCleanupWorker runs background cleanup of stale downloads
// Extracted from init() cleanup goroutine in main.go (lines 731-755)
func (s *downloadService) startCleanupWorker() {
	ticker := time.NewTicker(60 * time.Second) // Cleanup every minute
	defer ticker.Stop()

	for range ticker.C {
		s.cleanupStaleDownloads()
	}
}

// cleanupStaleDownloads removes stale download tracking entries
// Extracted from cleanupStaleDownloads in main.go (lines 742-755)
func (s *downloadService) cleanupStaleDownloads() {
	staleThreshold := 10 * time.Minute
	now := time.Now()

	s.activeDownloads.Range(func(key, value interface{}) bool {
		if startTime, ok := value.(time.Time); ok {
			if now.Sub(startTime) > staleThreshold {
				s.activeDownloads.Delete(key)
				s.logger.Printf("Cleaned up stale download: %s", key)
			}
		}
		return true
	})
}

// getClientIP extracts client IP from request headers
// Extracted from getClientIP in main.go (lines 549-558)
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