# vadapav-serve

A high-performance Go proxy server for Teldrive with optimized downloads and direct file ID access.

## Features

### 🚀 Performance Optimizations
- **Connection Pooling**: Reuses HTTP connections for better throughput
- **Large Buffers**: Configurable streaming buffers (default 256KB)
- **Download Manager Support**: Optimized for aria2c, wget, curl, etc.
- **Range Request Support**: Parallel download capabilities
- **Smart Error Handling**: Suppresses normal disconnection noise

### 📁 Direct File Access
- **Direct ID Downloads**: `/dl/<fileid>` (clean URLs for all downloads)
- **Directory Browsing**: Navigate folders through web interface  
- **No Path Encoding**: No more issues with special characters in filenames

### ⚡ Download Manager Compatibility
- Works perfectly with aria2c (no more "broken pipe" errors)
- Supports all popular download managers
- Clean logs with `LOG_CONNECTION_ERRORS=false`

### 🔧 Configuration
- Environment variable based configuration
- Upload page toggle
- Configurable buffer sizes
- Logging control

## Quick Start

1. **Setup Environment**:
```bash
cp .env.example .env
# Edit .env with your Teldrive URL and token
```

2. **Run the Server**:
```bash
go run main.go
# Or build: go build -o vadapav-serve main.go && ./vadapav-serve
```

3. **Download with aria2c**:
```bash
# All downloads use direct file IDs
aria2c -x 8 -s 8 "http://localhost:8888/dl/ABC123XYZ"
```

## Environment Variables

```bash
# Required
TELDRIVE_URL="http://your-teldrive-server:port"
TELDRIVE_TOKEN="your-jwt-token"

# Optional
ENABLE_UPLOAD_PAGE=true           # Show upload functionality
STREAM_BUFFER_SIZE=256           # Buffer size in KB (128, 256, 512, 1024)
LOG_CONNECTION_ERRORS=false      # Reduce aria2c log noise
```

## Usage Examples

### Web Interface
- Browse files: `http://localhost:8888/`
- Click filenames for direct downloads via `/dl/<fileid>`
- Upload page: `http://localhost:8888/upload` (if enabled)

### Download Managers
```bash
# aria2c with 8 connections
aria2c -x 8 -s 8 "http://localhost:8888/dl/FILE_ID"

# wget
wget "http://localhost:8888/dl/FILE_ID"

# curl with resume support  
curl -C - -O "http://localhost:8888/dl/FILE_ID"
```

## Architecture

- **Go 1.22+** with minimal dependencies
- **Single Binary** deployment
- **Template-based** web interface
- **RESTful API** design
- **Production Ready** with proper error handling

## Performance Tuning

For different network speeds:
- **Fast (1Gbps+)**: `STREAM_BUFFER_SIZE=512` or `1024`
- **Standard**: `STREAM_BUFFER_SIZE=256` (default)
- **Slower**: `STREAM_BUFFER_SIZE=128`

## Documentation

- [Performance Optimizations](PERFORMANCE.md)

## License

MIT License - see source code for details.
