package models

// TemplateEntry represents a file/directory entry for template rendering
type TemplateEntry struct {
	IsDir  bool   `json:"is_dir"`
	URL    string `json:"url"`
	Leaf   string `json:"leaf"`
	Size   int64  `json:"size"`
	FileID string `json:"file_id"`
}

// TemplateBreadcrumb represents a breadcrumb navigation item
type TemplateBreadcrumb struct {
	Link string `json:"link"`
	Text string `json:"text"`
}

// TemplateData represents the data passed to HTML templates
type TemplateData struct {
	Entries          []TemplateEntry      `json:"entries"`
	Breadcrumb       []TemplateBreadcrumb `json:"breadcrumb"`
	EnableUploadPage bool                 `json:"enable_upload_page"`
}

// NewTemplateEntry creates a new TemplateEntry from a TeldriveFile
func NewTemplateEntry(file *TeldriveFile, basePath string) *TemplateEntry {
	entry := &TemplateEntry{
		IsDir:  file.IsDirectory(),
		Leaf:   file.Name,
		Size:   file.Size,
		FileID: file.ID,
	}

	if entry.IsDir {
		// For directories, set the URL for browsing
		if basePath == "" {
			entry.URL = "/" + file.Name + "/"
		} else {
			entry.URL = "/" + basePath + "/" + file.Name + "/"
		}
	}

	return entry
}