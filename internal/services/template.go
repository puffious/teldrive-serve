package services

import (
	"html/template"
	"net/http"
	"path"
	"sort"
	"strings"

	"teldrive-go-proxy/internal/models"
	"teldrive-go-proxy/pkg/logger"
)

// templateService implements the TemplateService interface
type templateService struct {
	templates *template.Template
	logger    *logger.Logger
}

// NewTemplateService creates a new TemplateService
func NewTemplateService(templates *template.Template, log *logger.Logger) TemplateService {
	return &templateService{
		templates: templates,
		logger:    log,
	}
}

// RenderDirectory renders the directory listing page
// Extracted from renderDirectory() in main.go (lines 602-637)
func (s *templateService) RenderDirectory(w http.ResponseWriter, data *models.TemplateData) error {
	// Sort entries: directories first, then files, alphabetically within each group
	sort.SliceStable(data.Entries, func(i, j int) bool {
		if data.Entries[i].IsDir != data.Entries[j].IsDir {
			return data.Entries[i].IsDir
		}
		return strings.ToLower(data.Entries[i].Leaf) < strings.ToLower(data.Entries[j].Leaf)
	})

	if err := s.templates.Execute(w, data); err != nil {
		s.logger.Printf("Error executing template: %v", err)
		return err
	}

	return nil
}

// RenderUploadPage renders the upload page (if enabled)
func (s *templateService) RenderUploadPage(w http.ResponseWriter) error {
	return s.templates.ExecuteTemplate(w, "upload.html", nil)
}

// BuildTemplateData creates TemplateData from files and path
// This combines logic from main.go renderDirectory function
func (s *templateService) BuildTemplateData(files []models.TeldriveFile, cleanPath string, enableUploadPage bool) *models.TemplateData {
	data := &models.TemplateData{
		Entries:          make([]models.TemplateEntry, 0, len(files)),
		Breadcrumb:       buildBreadcrumb(cleanPath),
		EnableUploadPage: enableUploadPage,
	}

	for _, file := range files {
		isDir := file.IsDirectory()
		var itemURL string
		if isDir {
			itemURL = "/" + path.Join(cleanPath, file.Name) + "/"
		}

		data.Entries = append(data.Entries, models.TemplateEntry{
			IsDir:  isDir,
			URL:    itemURL, // Only used for directories
			Leaf:   file.Name,
			Size:   file.Size,
			FileID: file.ID,
		})
	}

	return data
}

// buildBreadcrumb builds breadcrumb navigation
// Extracted from buildBreadcrumb() in main.go (lines 693-709)
func buildBreadcrumb(cleanPath string) []models.TemplateBreadcrumb {
	if cleanPath == "" {
		return nil
	}

	parts := strings.Split(cleanPath, "/")
	breadcrumbs := make([]models.TemplateBreadcrumb, 0, len(parts)+1)
	breadcrumbs = append(breadcrumbs, models.TemplateBreadcrumb{Link: "/", Text: "root"})

	currentPath := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		currentPath = path.Join(currentPath, part)
		breadcrumbs = append(breadcrumbs, models.TemplateBreadcrumb{Link: "/" + currentPath, Text: part})
	}

	return breadcrumbs
}