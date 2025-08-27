package config

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// TestTeldriveConnectivity tests connection to Teldrive server
func (c *Config) TestTeldriveConnectivity() error {
	if c.Features.SkipConnectivityCheck {
		return nil
	}

	testURL := strings.TrimSuffix(c.Teldrive.URL, "/") + "/api/version"
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

// StreamBufferSizeBytes returns stream buffer size in bytes
func (c *Config) StreamBufferSizeBytes() int {
	return c.Features.StreamBufferSize * 1024 // Convert KB to bytes
}

// String returns a string representation of key config values (for logging)
func (c *Config) String() string {
	return fmt.Sprintf("Server(:%d) Teldrive(%s) Upload(%t) MaxDL(%d) Buffer(%dKB)",
		c.Server.Port,
		c.Teldrive.URL,
		c.Features.EnableUploadPage,
		c.Features.MaxConcurrentDownloads,
		c.Features.StreamBufferSize,
	)
}