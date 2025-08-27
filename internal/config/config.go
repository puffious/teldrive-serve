package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all application configuration
type Config struct {
	Server   ServerConfig
	Teldrive TeldriveConfig
	Features FeatureConfig
}

// ServerConfig holds server-specific configuration
type ServerConfig struct {
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

// TeldriveConfig holds Teldrive API configuration
type TeldriveConfig struct {
	URL    string
	Token  string
	APIURL string // Derived from URL
}

// FeatureConfig holds feature flags and optional settings
type FeatureConfig struct {
	EnableUploadPage         bool
	MaxConcurrentDownloads   int
	StreamBufferSize         int
	LogConnectionErrors      bool
	SkipConnectivityCheck    bool
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	// Load .env file if it exists (ignore errors like the original code)
	godotenv.Load()

	config := &Config{}

	// Load server config
	config.Server = ServerConfig{
		Port:            getEnvInt("PORT", 8888),
		ReadTimeout:     30 * time.Second,
		WriteTimeout:    0, // No write timeout for downloads
		IdleTimeout:     120 * time.Second,
		ShutdownTimeout: 30 * time.Second,
	}

	// Load Teldrive config
	teldriveURL := os.Getenv("TELDRIVE_URL")
	teldriveToken := os.Getenv("TELDRIVE_TOKEN")
	
	if teldriveURL == "" {
		return nil, fmt.Errorf("TELDRIVE_URL is required but not set")
	}
	if teldriveToken == "" {
		return nil, fmt.Errorf("TELDRIVE_TOKEN is required but not set")
	}

	// Validate URL format
	if _, err := url.Parse(teldriveURL); err != nil {
		return nil, fmt.Errorf("TELDRIVE_URL is not a valid URL: %v", err)
	}

	config.Teldrive = TeldriveConfig{
		URL:    teldriveURL,
		Token:  teldriveToken,
		APIURL: strings.TrimSuffix(teldriveURL, "/") + "/api",
	}

	// Load feature config
	config.Features = FeatureConfig{
		EnableUploadPage:         getEnvBool("ENABLE_UPLOAD_PAGE", false),
		MaxConcurrentDownloads:   getEnvInt("MAX_CONCURRENT_DOWNLOADS", 50),
		StreamBufferSize:         getEnvInt("STREAM_BUFFER_SIZE", 256),
		LogConnectionErrors:      getEnvBool("LOG_CONNECTION_ERRORS", false),
		SkipConnectivityCheck:    getEnvBool("SKIP_CONNECTIVITY_CHECK", false),
	}

	return config, nil
}

// getEnvInt gets an integer from environment variable with default
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return defaultValue
}

// getEnvBool gets a boolean from environment variable with default
func getEnvBool(key string, defaultValue bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return defaultValue
	}
	return value == "true" || value == "1" || value == "yes"
}

// Validate validates the configuration
func (c *Config) Validate() error {
	// Basic validation - most is already done in Load()
	if c.Features.StreamBufferSize <= 0 {
		return fmt.Errorf("STREAM_BUFFER_SIZE must be positive, got %d", c.Features.StreamBufferSize)
	}
	if c.Features.MaxConcurrentDownloads <= 0 {
		return fmt.Errorf("MAX_CONCURRENT_DOWNLOADS must be positive, got %d", c.Features.MaxConcurrentDownloads)
	}
	return nil
}