# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Architecture

This is a high-performance Go proxy server for Teldrive with a modular, maintainable architecture:

- **Modular Design** - Clean separation into packages (`internal/`, `pkg/`)
- **Service Layer** - Business logic in dedicated services (FileService, DownloadService, etc.)
- **Dependency Injection** - Centralized app container with interface-based dependencies
- **Template-based web interface** - HTML templates in `templates/` directory
- **RESTful API design** - Proxies requests to upstream Teldrive server
- **Performance-optimized** - Configurable buffers, connection pooling, retry logic

### Core Features
- Direct file access via `/dl/<fileid>` endpoints (no path encoding issues)  
- Directory browsing through web interface at `/`
- Optional upload functionality via `/upload` page
- Download manager compatibility (aria2c, wget, curl) with range request support
- Connection pooling and large streaming buffers (256KB-1MB) for high throughput

### Key Data Flow
1. **Configuration**: `internal/config` loads and validates environment variables
2. **Service Layer**: Business logic handled by dedicated services:
   - `FileService`: File validation and metadata operations
   - `TeldriveClient`: API communication with Bearer token authentication
   - `TemplateService`: HTML rendering and breadcrumb generation
   - `DownloadService`: Streaming, rate limiting, and retry logic
3. **Web Interface**: Directory browsing and file downloads
4. **Performance**: Large buffers (256KB-1MB) and connection reuse for high throughput

## Commands

### Development
```bash
# Run the server directly
go run main.go

# Build binary
go build -o vadapav-serve main.go

# Run built binary  
./vadapav-serve
```

### Docker
```bash
# Build image
docker build -t vadapav-serve .

# Run with docker-compose
docker-compose up
```

### Environment Setup
```bash
# Copy example config
cp .env.example .env

# Required environment variables:
TELDRIVE_URL="http://your-teldrive-server:port"
TELDRIVE_TOKEN="your-jwt-token"

# Optional tuning:
ENABLE_UPLOAD_PAGE=true           # Show upload functionality  
STREAM_BUFFER_SIZE=256           # Buffer size in KB (128, 256, 512, 1024)
LOG_CONNECTION_ERRORS=false      # Reduce download manager log noise
MAX_CONCURRENT_DOWNLOADS=50      # Connection limiting
```

## Architecture Details

### File Structure
```
vadapav-serve/
├── main.go                     # Legacy entry point (being refactored)
├── internal/
│   ├── app/                   # Application container and DI setup
│   ├── config/               # Configuration management
│   ├── handlers/             # HTTP request handlers (future)
│   ├── services/             # Business logic services
│   │   ├── file.go          # File operations service
│   │   ├── template.go      # Template rendering service
│   │   └── interfaces.go    # Service interfaces
│   ├── client/               # External API clients
│   │   └── teldrive.go      # Teldrive API client
│   └── models/               # Data structures
├── pkg/
│   ├── errors/              # Custom error types
│   └── logger/              # Structured logging
├── templates/               # HTML templates
└── docs/                   # Documentation and planning
```

### HTTP Client Optimization
The application uses a heavily tuned HTTP client:
- 200 max idle connections, 50 per host
- 256KB read/write buffers  
- 120s keep-alive, 300s idle timeout
- Compression disabled for file transfers
- No request timeout for downloads

### Download Flow
1. Validate file ID format (UUID pattern)
2. Rate limiting via semaphore (50 concurrent by default)
3. Get file metadata via `/files/{id}` API
4. Stream via `/files/{id}/{name}?download=1` endpoint  
5. Retry up to 3 times with exponential backoff
6. Use 1MB streaming buffer for optimal throughput

### Error Handling
- Connection errors (broken pipe, reset) are suppressed to reduce aria2c log noise
- Graceful shutdown with 30s timeout
- Stale download cleanup every 60 seconds
- Active download tracking to prevent resource exhaustion

## Performance Tuning

Buffer sizes by network speed:
- Fast (1Gbps+): `STREAM_BUFFER_SIZE=512` or `1024`  
- Standard: `STREAM_BUFFER_SIZE=256` (default)
- Slower: `STREAM_BUFFER_SIZE=128`

The server is optimized for high-speed downloads with minimal CPU usage and connection reuse.

## Development Guidelines

### Code Organization
- **Services**: Add new business logic to `internal/services/`
- **Models**: Define data structures in `internal/models/`
- **Configuration**: Update `internal/config/` for new environment variables
- **Interfaces**: Define contracts in service interface files

### Testing
- Unit tests for services with mocked dependencies
- Integration tests for full request flows
- Mock external dependencies (TeldriveClient interface)

### Refactoring Status
- ✅ **Phase 1**: Foundation architecture (packages, models, config)
- 🔄 **Phase 2**: Service layer implementation (in progress)
- ⏳ **Phase 3**: HTTP handler refactoring
- ⏳ **Phase 4**: Complete migration from main.go
- ⏳ **Phase 5**: Comprehensive testing

### Commit Guidelines
- Always make small commits to changes
- Remember to update docs on regular basis
- Update CLAUDE.md when architecture changes