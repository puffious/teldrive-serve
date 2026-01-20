# Gunicorn configuration optimized for high-throughput streaming

import multiprocessing
import os

# Worker configuration
workers = int(os.getenv("WORKERS", multiprocessing.cpu_count() * 2))
worker_class = "gthread"  # Threaded workers for better I/O performance than gevent
threads = 4  # 4 threads per worker for concurrent downloads

# Connection settings
worker_connections = 2000
max_requests = 0  # Disable worker recycling for persistent connections
max_requests_jitter = 0

# Timeout settings
timeout = 0  # Disable worker timeout for long downloads
graceful_timeout = 30
keepalive = 5

# Server socket
bind = "0.0.0.0:8888"
backlog = 2048

# Logging
accesslog = "-" if os.getenv("DISABLE_LOGS", "false").lower() != "true" else None
errorlog = "-" if os.getenv("DISABLE_LOGS", "false").lower() != "true" else None
loglevel = "error"

# Performance optimizations
preload_app = False  # Don't preload to avoid sharing sessions
sendfile = True  # Use sendfile() for better performance
reuse_port = True  # Allow multiple workers to bind to the same port

# Process naming
proc_name = "teldrive-proxy"

def on_starting(server):
    """Called just before the master process is initialized."""
    if os.getenv("DISABLE_LOGS", "false").lower() != "true":
        print(f"Starting Gunicorn with {workers} workers, {threads} threads each")

def worker_int(worker):
    """Called when a worker receives the SIGINT or SIGQUIT signal."""
    worker.log.info("Worker received INT or QUIT signal")

def worker_abort(worker):
    """Called when a worker receives the SIGABRT signal."""
    worker.log.info("Worker received SIGABRT signal")
