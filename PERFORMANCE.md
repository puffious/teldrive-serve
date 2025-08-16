# Performance Optimizations

This document outlines the simple but effective optimizations implemented to improve download speeds and compatibility with download managers.

## HTTP Client Optimizations

1. **Connection Pooling**: Configured with `MaxIdleConns: 100` and `MaxIdleConnsPerHost: 20` to reuse connections
2. **Connection Limits**: `MaxConnsPerHost: 50` to handle download managers that make many concurrent requests
3. **Keep-Alive**: Set to 30 seconds to maintain connections for multiple requests
4. **Compression Disabled**: `DisableCompression: true` for file transfers to avoid CPU overhead
5. **No Download Timeout**: Removed timeout for file downloads to handle large files

## Buffer Size Optimizations

1. **Streaming Buffer**: Increased from 64KB to configurable size (default 256KB)
2. **Copy Buffer**: Dynamically calculated based on stream buffer size (minimum 32KB)
3. **Environment Variable**: `STREAM_BUFFER_SIZE` allows tuning for different network conditions

## HTTP Header Optimizations

1. **Cache Headers**: Added `Cache-Control: public, max-age=3600` for better client-side caching
2. **Conditional Requests**: Forwarding `If-Modified-Since` and `If-None-Match` headers
3. **User-Agent Forwarding**: Pass client User-Agent to upstream server
4. **Extended Header Copying**: Including `cache-control` and `expires` headers from upstream

## Download Manager Compatibility

1. **Connection Error Handling**: Suppressed "broken pipe" and "unexpected EOF" errors which are normal with download managers
2. **Range Request Support**: Proper forwarding of HTTP Range headers for parallel downloads
3. **Configurable Error Logging**: `LOG_CONNECTION_ERRORS=false` reduces noise from normal disconnections

## Recommended Settings

For different network conditions:

- **Fast Networks (1Gbps+)**: `STREAM_BUFFER_SIZE=512` or `1024`
- **Standard Networks**: `STREAM_BUFFER_SIZE=256` (default)
- **Slower Networks**: `STREAM_BUFFER_SIZE=128`

For download manager usage:
- **aria2c**: Set `LOG_CONNECTION_ERRORS=false` to reduce log noise
- **wget/curl**: Default settings work well
- **IDM/Browser**: Default settings optimal

## Performance Impact

These simple optimizations provide:
- Reduced connection overhead through pooling
- Better throughput with larger buffers
- Improved client-side caching
- Lower CPU usage by disabling compression for file transfers
- Cleaner logs when using download managers

All changes maintain the simple, straightforward architecture while maximizing performance and compatibility.
