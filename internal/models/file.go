package models

// TeldriveFile represents a file or directory from Teldrive API
type TeldriveFile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

// TeldriveListResponse represents the response from Teldrive list API
type TeldriveListResponse struct {
	Items []TeldriveFile `json:"items"`
}

// IsDirectory returns true if the file is a directory
func (f *TeldriveFile) IsDirectory() bool {
	return f.Type == "folder"
}

// IsFile returns true if the file is a regular file
func (f *TeldriveFile) IsFile() bool {
	return !f.IsDirectory()
}