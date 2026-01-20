# Teldrive Streaming Proxy

High-performance Python proxy for streaming files from Teldrive.

## Performance Optimization

Current optimizations provide **13-14MB/s** per connection. To achieve true gigabit speeds (100-125MB/s), consider:

### Option 1: Kernel-Level Optimizations (Linux only)
Enable TCP BBR congestion control on your host:
```bash
# On the Docker host (requires privileged access)
sudo sysctl -w net.core.default_qdisc=fq
sudo sysctl -w net.ipv4.tcp_congestion_control=bbr
sudo sysctl -w net.core.rmem_max=134217728
sudo sysctl -w net.core.wmem_max=134217728
```

### Option 2: Multiple Concurrent Connections
Most download managers can use 4-16 concurrent connections, which would give you:
- 4 connections × 14MB/s = **56MB/s**
- 8 connections × 14MB/s = **112MB/s** (near gigabit!)

### Option 3: Nginx Reverse Proxy (Recommended for Production)
Use Nginx in front for better streaming performance:
```nginx
server {
    listen 80;
    
    location / {
        proxy_pass http://localhost:8888;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        
        # High-performance settings
        tcp_nodelay on;
        tcp_nopush on;
        sendfile on;
        sendfile_max_chunk 8m;
        
        # Large buffers for streaming
        proxy_buffer_size 128k;
        proxy_buffers 4 256k;
        proxy_busy_buffers_size 256k;
    }
}
```

### Option 4: Switch to Go/Rust (Maximum Performance)
For true multi-gigabit speeds, a compiled language proxy is recommended. The Python GIL limits single-connection throughput to ~15-20MB/s regardless of optimization.

## Environment Variables

```bash
TELDRIVE_URL=http://your-teldrive:8080
TELDRIVE_TOKEN=your_access_token
DISABLE_LOGS=true  # Disable logging for maximum performance
WORKERS=8          # Number of gunicorn workers (default: CPU count × 2)
```

## Current Optimizations Applied

- ✅ Threaded workers (gthread) for I/O performance
- ✅ 8MB chunk size for reduced Python overhead
- ✅ Connection pooling (50 pools, 100 connections each)
- ✅ Raw socket streaming with zero-copy when possible
- ✅ No worker timeouts for long downloads
- ✅ Increased TCP buffer sizes (128MB)
- ✅ 8 threads per worker for concurrency
- ✅ TCP keepalive for persistent connections

## Theoretical Limits

- **Single connection**: ~15-20MB/s (Python GIL limitation)
- **8 concurrent connections**: ~120MB/s (near gigabit)
- **With Nginx**: ~200-500MB/s potential
- **Go/Rust implementation**: 1-10Gbps+ potential
