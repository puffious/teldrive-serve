# Phase 2: Service Layer - Detailed Tasks

## Objective
Extract business logic from main.go into modular, testable services while maintaining existing functionality.

## Task 1: TeldriveClient Service (`internal/client/`)
**Status**: ✅ Complete
**Objective**: Abstract Teldrive API interactions
**Files**:
- `teldrive.go` - HTTP client for Teldrive API
**Extract from main.go**:
- `getTeldriveItems()` function (lines 639-663)
- `getTeldriveFileMetadata()` function (lines 665-691)
- HTTP request setup with Bearer token authentication

## Task 2: FileService (`internal/services/`)
**Status**: ✅ Complete  
**Objective**: Handle file operations and metadata
**Files**:
- `file.go` - File service implementation
**Extract from main.go**:
- File ID validation logic (fileIDRegex)
- Directory listing functionality
- File metadata retrieval

## Task 3: TemplateService (`internal/services/`)
**Status**: ✅ Complete
**Objective**: Handle HTML template rendering
**Files**:
- `template.go` - Template service implementation  
**Extract from main.go**:
- `renderDirectory()` function (lines 602-637)
- Breadcrumb building (`buildBreadcrumb()`, lines 693-709)
- Template execution with error handling

## Task 4: DownloadService (`internal/services/`)
**Status**: ⏳ Pending
**Objective**: Manage download logic, rate limiting, and streaming
**Files**:
- `download.go` - Download service implementation
**Extract from main.go**:
- Rate limiting logic (`downloadSemaphore`, `activeDownloads`)
- Download streaming (`streamWithRetry`, `attemptSingleDownload`)
- Connection error handling (`isConnectionError`)
- Download tracking and cleanup

## Task 5: Update App Container (`internal/app/`)
**Status**: ⏳ Pending
**Objective**: Wire up service implementations in DI container
**Changes**:
- Initialize concrete service implementations in `New()` function
- Replace interface placeholders with actual services

## Task 6: Create Service Tests (`tests/`)
**Status**: ⏳ Pending
**Objective**: Add unit tests for each service
**Files**:
- `tests/services/file_test.go`
- `tests/services/download_test.go` 
- `tests/services/template_test.go`
- `tests/client/teldrive_test.go`

## Implementation Order
1. ✅ TeldriveClient (no dependencies on other services)
2. ✅ FileService (uses TeldriveClient)
3. ✅ TemplateService (uses models, no external deps)
4. ⏳ DownloadService (uses TeldriveClient, FileService)
5. ⏳ Update App Container (wire everything together)
6. ⏳ Add service tests

## Success Criteria
- All services implement their respective interfaces
- Business logic extracted from main.go without breaking existing functionality
- Services are unit testable with mockable dependencies
- App container properly initializes all services
- No regression in download performance or functionality