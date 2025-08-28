package services

import (
	"context"
	"regexp"

	"teldrive-go-proxy/internal/models"
	"teldrive-go-proxy/pkg/errors"
	"teldrive-go-proxy/pkg/logger"
)

// fileService implements the FileService interface
type fileService struct {
	client     TeldriveClient
	logger     *logger.Logger
	fileIDRegex *regexp.Regexp
}

// NewFileService creates a new FileService
func NewFileService(client TeldriveClient, log *logger.Logger) FileService {
	// File ID validation regex (extracted from main.go line 44)
	fileIDRegex := regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
	
	return &fileService{
		client:      client,
		logger:      log,
		fileIDRegex: fileIDRegex,
	}
}

// GetFileMetadata retrieves metadata for a file by ID
func (s *fileService) GetFileMetadata(ctx context.Context, fileID string) (*models.TeldriveFile, error) {
	// Validate file ID format for security (from main.go lines 324-329)
	if !s.fileIDRegex.MatchString(fileID) {
		return nil, errors.New("INVALID_FILE_ID", "Invalid file ID format")
	}

	file, err := s.client.GetFile(ctx, fileID)
	if err != nil {
		return nil, errors.Wrap(err, "FILE_NOT_FOUND", "Failed to get file metadata")
	}

	return file, nil
}

// ListDirectory lists files in a directory path
func (s *fileService) ListDirectory(ctx context.Context, path string) ([]models.TeldriveFile, error) {
	files, err := s.client.ListFiles(ctx, path)
	if err != nil {
		return nil, errors.Wrap(err, "DIRECTORY_LIST_FAILED", "Failed to list directory")
	}

	return files, nil
}

// ValidateFileID checks if a file ID has valid format
func (s *fileService) ValidateFileID(fileID string) bool {
	return s.fileIDRegex.MatchString(fileID)
}