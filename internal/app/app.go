package app

import (
	"html/template"
	"net"
	"net/http"
	"time"

	"teldrive-go-proxy/internal/config"
	"teldrive-go-proxy/internal/services"
	"teldrive-go-proxy/pkg/logger"
)

// App represents the main application with all dependencies
type App struct {
	Config    *config.Config
	Logger    *logger.Logger
	Templates *template.Template

	// Services will be implemented in Phase 2
	FileService     services.FileService
	DownloadService services.DownloadService
	TemplateService services.TemplateService
	TeldriveClient  services.TeldriveClient

	// HTTP client for external requests
	HTTPClient *http.Client

	// Rate limiting
	DownloadSemaphore chan struct{}
}

// New creates a new App instance with all dependencies
func New() (*App, error) {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// Create logger
	log := logger.New(cfg.Features.LogConnectionErrors)

	// Load templates (same as original main.go)
	templates, err := loadTemplates()
	if err != nil {
		return nil, err
	}

	// Create HTTP client (same as original setupHTTPClient)
	httpClient := createHTTPClient(cfg)

	// Create rate limiting semaphore
	downloadSemaphore := make(chan struct{}, cfg.Features.MaxConcurrentDownloads)

	app := &App{
		Config:            cfg,
		Logger:            log,
		Templates:         templates,
		HTTPClient:        httpClient,
		DownloadSemaphore: downloadSemaphore,
	}

	// Test Teldrive connectivity if not skipped
	if err := cfg.TestTeldriveConnectivity(); err != nil {
		app.Logger.Printf("Warning: Teldrive connectivity test failed: %v", err)
	}

	app.Logger.Printf("Application initialized: %s", cfg.String())

	return app, nil
}

// loadTemplates loads HTML templates (extracted from main.go)
func loadTemplates() (*template.Template, error) {
	return template.ParseFiles(
		"templates/index.html",
		"templates/upload.html",
	)
}

// createHTTPClient creates an optimized HTTP client (extracted from main.go setupHTTPClient)
func createHTTPClient(cfg *config.Config) *http.Client {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 120 * time.Second, // Longer keep-alive for better connection reuse
		}).DialContext,
		MaxIdleConns:          200,               // Increased for better connection pooling
		MaxIdleConnsPerHost:   50,                // Increased per-host connections for high-throughput
		IdleConnTimeout:       300 * time.Second, // Longer idle timeout
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableCompression:    true, // No compression for files - saves CPU and improves speed
		DisableKeepAlives:     false,
		MaxConnsPerHost:       100,                          // Increased max connections per host for better throughput
		ReadBufferSize:        cfg.StreamBufferSizeBytes(),  // Use configured buffer size
		WriteBufferSize:       cfg.StreamBufferSizeBytes(),  // Use configured buffer size
	}

	return &http.Client{
		Transport: transport,
		Timeout:   0, // No timeout for downloads
	}
}

// Close gracefully shuts down the application
func (a *App) Close() error {
	// Future: close database connections, stop background workers, etc.
	a.Logger.Printf("Application shutting down...")
	return nil
}