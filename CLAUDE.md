# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Architecture

This is a high-performance Go proxy server for Teldrive with the following key components:

- **Single Go binary** (`main.go`) - ~800 lines containing the entire application
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
1. Web requests hit browse handler for directory listing
2. File downloads use direct ID handler `/dl/<fileid>` 
3. Backend communicates with Teldrive API using Bearer token authentication
4. Streaming downloads use large buffers and connection reuse for performance

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
- `main.go` - Complete application (initialization, handlers, HTTP client setup)
- `templates/index.html` - Directory browsing interface with dark/light theme
- `templates/upload.html` - Upload form (if enabled)
- `Dockerfile` - Multi-stage build for minimal production image
- `go.mod` - Go 1.22+ with minimal dependencies (just godotenv)

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
- Always make small commits to changes