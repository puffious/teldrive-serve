import os
import requests
from requests.adapters import HTTPAdapter
from urllib3.util.retry import Retry
from urllib3.exceptions import ProtocolError
from http.client import IncompleteRead
from dotenv import load_dotenv
from flask import Flask, Response, request, render_template, abort, redirect, url_for

# --- Configuration ---
load_dotenv()
TELDRIVE_URL = os.getenv("TELDRIVE_URL")
TELDRIVE_TOKEN = os.getenv("TELDRIVE_TOKEN")
TELDRIVE_HASH = os.getenv("TELDRIVE_HASH")
TELDRIVE_DL_URL = os.getenv("TELDRIVE_DL_URL")
DISABLE_LOGS = os.getenv("DISABLE_LOGS", "false").lower() == "true"

if not TELDRIVE_URL or not TELDRIVE_TOKEN or not TELDRIVE_HASH:
    raise ValueError("TELDRIVE_URL, TELDRIVE_TOKEN, and TELDRIVE_HASH must be set in the .env file.")
TELDRIVE_API_URL = f"{TELDRIVE_URL.rstrip('/')}/api"
HTTP_PROXY_PORT = 8888
app = Flask(__name__)

def log(message):
    """Conditional logging based on DISABLE_LOGS env var"""
    if not DISABLE_LOGS:
        print(message)

# --- Session with connection pooling and keep-alive ---
def create_session():
    """Create a persistent session with connection pooling and retry logic"""
    session = requests.Session()
    
    # Configure retry strategy for transient failures
    retry_strategy = Retry(
        total=2,  # Retry up to 2 times (reduced from 3)
        backoff_factor=0.1,  # Wait 0.1s, 0.2s between retries (reduced from 0.3s)
        status_forcelist=[429, 500, 502, 503, 504],
        allowed_methods=["HEAD", "GET", "OPTIONS"],
        raise_on_status=False  # Don't raise immediately, let us handle it
    )
    
    # Configure adapter with aggressive connection pooling
    adapter = HTTPAdapter(
        max_retries=retry_strategy,
        pool_connections=100,   # Increased from 50 for better connection reuse
        pool_maxsize=200,       # Increased from 100 for high concurrency
        pool_block=False
    )
    
    session.mount("http://", adapter)
    session.mount("https://", adapter)
    
    # Aggressive keep-alive and performance headers
    session.headers.update({
        'Connection': 'keep-alive',
        'Keep-Alive': '600',  # Increased from 300 for longer connection reuse
        'Accept-Encoding': 'gzip, deflate',  # Enable compression for metadata
    })
    
    return session

# Create global session for connection reuse
session = create_session()

# File metadata cache to avoid repeated API calls
# Format: {file_id: {'data': file_item, 'timestamp': unix_time}}
from time import time
file_metadata_cache = {}
CACHE_TTL = 300  # 5 minutes cache

def get_teldrive_items(path):
    """Fetch items for a path and return (items, status_code)."""
    api_endpoint = f"{TELDRIVE_API_URL}/files"
    headers = {"Authorization": f"Bearer {TELDRIVE_TOKEN}"}
    params = {"path": path, "limit": 1000}
    try:
        response = session.get(api_endpoint, headers=headers, params=params, timeout=10)
        status_code = response.status_code
        if status_code == 404:
            # Don't log every 404 - these are expected for invalid paths
            return [], status_code
        response.raise_for_status()
        return response.json().get("items", []), status_code
    except requests.exceptions.Timeout:
        log(f"Timeout fetching from Teldrive API (Path: {path})")
        abort(504, description="Teldrive backend timeout")
    except requests.exceptions.RequestException as e:
        log(f"Error fetching from Teldrive API (Path: {path}): {e}")
        abort(502, description="Could not connect to the Teldrive backend.")

def get_teldrive_file_by_id(file_id, use_cache=True):
    """Get a file directly by its ID from Teldrive API with optional caching"""
    # Check cache first
    if use_cache and file_id in file_metadata_cache:
        cached = file_metadata_cache[file_id]
        if time() - cached['timestamp'] < CACHE_TTL:
            return cached['data']
    
    api_endpoint = f"{TELDRIVE_API_URL}/files/{file_id}"
    headers = {"Authorization": f"Bearer {TELDRIVE_TOKEN}"}
    
    try:
        response = session.get(api_endpoint, headers=headers, timeout=10)
        if response.status_code == 404:
            return None
        response.raise_for_status()
        file_data = response.json()
        
        # Cache the result
        if use_cache:
            file_metadata_cache[file_id] = {'data': file_data, 'timestamp': time()}
        
        return file_data
    except requests.exceptions.RequestException as e:
        print(f"Error fetching file by ID from Teldrive API (ID: {file_id}): {e}")
        abort(502, description="Could not connect to the Teldrive backend.")

@app.route('/dl/<file_id>')
def direct_download(file_id):
    # Proxy the direct Teldrive download URL
    file_item = get_teldrive_file_by_id(file_id, use_cache=True)
    if not file_item:
        abort(404, description="File not found")
    
    # Construct direct download URL with hash parameter
    file_name = file_item['name']
    download_url = f"{TELDRIVE_DL_URL.rstrip('/')}/api/files/{file_id}/{file_name}?hash={TELDRIVE_HASH}&download=1"
    
    # Forward range headers for resumable downloads
    headers = {}
    if 'Range' in request.headers:
        headers['Range'] = request.headers['Range']
    
    try:
        # Stream from Teldrive download URL
        response = session.get(download_url, headers=headers, stream=True, timeout=(10, None))
        response.raise_for_status()
        
        # Forward essential headers
        response_headers = {
            'Content-Type': response.headers.get('Content-Type', 'application/octet-stream'),
            'Content-Length': response.headers.get('Content-Length'),
            'Content-Range': response.headers.get('Content-Range'),
            'Accept-Ranges': response.headers.get('Accept-Ranges', 'bytes'),
            'Content-Disposition': f'attachment; filename="{file_name}"'
        }
        # Remove None values
        response_headers = {k: v for k, v in response_headers.items() if v is not None}
        
        # Stream response
        def generate():
            for chunk in response.iter_content(chunk_size=8 * 1024 * 1024):
                if chunk:
                    yield chunk
        
        return Response(generate(), status=response.status_code, headers=response_headers, direct_passthrough=True)
        
    except requests.exceptions.RequestException as e:
        log(f"Error proxying download for {file_id}: {e}")
        abort(502, description="Could not connect to the download server.")

# Update the routes for static files
@app.route('/site.webmanifest')
def webmanifest():
    return app.send_static_file('site.webmanifest')

@app.route('/favicon.ico')
def favicon():
    return app.send_static_file('img/favicon.ico')

@app.route('/', defaults={'path': ''})
@app.route('/<path:path>')
def browse_and_download(path):
    # Don't process /dl/ routes here
    if path.startswith('dl/'):
        abort(404)
        
    clean_path = path.strip('/')
    api_path = f"/{clean_path}"
    parent_dir = os.path.dirname(api_path) or '/'
    item_name = os.path.basename(clean_path)

    # Try to list the requested path first (avoids extra parent calls for valid folders)
    items, status_code = get_teldrive_items(api_path)

    # If the path is not a folder, check parent once to see if it's a file and redirect
    # But skip this check for UUID-like paths or single-letter paths that are likely invalid
    if status_code == 404 and clean_path:
        # Optimize: Don't waste an API call on obviously invalid patterns
        # - UUID pattern: paths like /f/8fd1c22c-43fd-4c92-ac84-0793df9d5605
        # - Single char dirs: /f, /e, etc. (unless they're common dirs)
        import re
        uuid_pattern = r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        
        # Check if this looks like someone is trying to access a UUID path
        path_parts = clean_path.split('/')
        is_uuid_like = any(re.match(uuid_pattern, part, re.IGNORECASE) for part in path_parts)
        
        # If it's a UUID-like path, skip the parent check (these are never valid folder structures)
        if is_uuid_like:
            log(f"Skipping parent check for UUID-like path: {api_path}")
            abort(404, description="Path not found")
        
        # Only check parent for paths that might actually be files
        parent_items, parent_status = get_teldrive_items(parent_dir)
        
        # If parent also doesn't exist, no point checking further
        if parent_status == 404:
            log(f"Parent directory not found: {parent_dir}")
            abort(404, description="Path not found")
            
        for item in parent_items:
            if item['name'] == item_name and item['type'] == 'file':
                return redirect(url_for('direct_download', file_id=item['id']))
        abort(404, description="Path not found")
    breadcrumb = []
    if clean_path:
        breadcrumb.append({'Link': '/', 'Text': 'root'})
        current_path_breadcrumb = ''
        for part in clean_path.split('/'):
            current_path_breadcrumb += f'/{part}'
            breadcrumb.append({'Link': current_path_breadcrumb, 'Text': part})

    entries = []
    for item in items:
        if item['type'] == 'folder':
            item_url = f"/{clean_path}/{item['name']}" if clean_path else f"/{item['name']}"
            item_url += '/'
            is_dir = True
        else:
            # Cache file metadata for faster downloads later
            file_metadata_cache[item['id']] = {'data': item, 'timestamp': time()}
            # Use the direct download URL for files - no need for query parameters
            item_url = f"/dl/{item['id']}"
            is_dir = False
        
        entries.append({
            'IsDir': is_dir,
            'URL': item_url,
            'Leaf': item['name'],
            'Size': item.get('size', 0)
        })
    entries.sort(key=lambda x: (not x['IsDir'], x['Leaf'].lower()))
    return render_template('index.html', Breadcrumb=breadcrumb, Entries=entries)

def stream_file(file_item, force_download=False):
    """
    Direct transparent streaming proxy - mimics teldrive's io.CopyN approach.
    No server-side retry logic - let the client handle resumption via Range requests.
    """
    file_id = file_item['id']
    file_name = file_item['name']
    stream_url = f"{TELDRIVE_API_URL}/files/{file_id}/{file_name}"
    
    # Forward ALL range and conditional headers transparently
    headers_to_forward = {}
    for header in ['Range', 'If-Range', 'If-None-Match', 'If-Modified-Since']:
        value = request.headers.get(header)
        if value:
            headers_to_forward[header] = value
    
    try:
        # Direct streaming request - no timeouts on read for maximum stability
        # Let TCP handle connection keepalive naturally
        td_response = session.get(
            stream_url,
            headers=headers_to_forward,
            cookies={"access_token": TELDRIVE_TOKEN},
            stream=True,
            allow_redirects=True,
            timeout=(10, None)  # 10s connect, no read timeout
        )
        td_response.raise_for_status()
        
        # Forward ALL response headers transparently
        response_headers = dict(td_response.headers)
        
        # Override Content-Disposition based on force_download flag
        disposition = 'attachment' if force_download else 'inline'
        response_headers['Content-Disposition'] = f'{disposition}; filename="{file_name}"'
        
        # Ensure critical headers are present
        if 'Accept-Ranges' not in response_headers:
            response_headers['Accept-Ranges'] = 'bytes'
        
        if 'Content-Type' not in response_headers:
            import mimetypes
            content_type = mimetypes.guess_type(file_name)[0] or 'application/octet-stream'
            response_headers['Content-Type'] = content_type
        
        # Ultra-high performance streaming with graceful handling of upstream dropouts
        def stream_passthrough():
            chunk_size = 8 * 1024 * 1024  # 8MB chunks to minimize Python overhead
            try:
                for chunk in td_response.iter_content(chunk_size=chunk_size):
                    if chunk:
                        yield chunk
            except (requests.exceptions.ChunkedEncodingError,
                    requests.exceptions.ConnectionError,
                    ProtocolError,
                    IncompleteRead) as e:
                # Upstream closed early; let client retry via Range requests
                log(f"Upstream stream interrupted for {file_id}: {e}")
                return
            except Exception as e:
                log(f"Unexpected streaming error for {file_id}: {e}")
                return
            finally:
                td_response.close()
        
        # Return response with same status code as teldrive (206 for ranges, 200 for full)
        return Response(
            stream_passthrough(),
            status=td_response.status_code,
            headers=response_headers,
            direct_passthrough=True
        )
        
    except requests.exceptions.RequestException as e:
        log(f"Error streaming from Teldrive (file_id: {file_id}): {e}")
        abort(502, description="Could not connect to the Teldrive backend.")

# Custom error handlers
@app.errorhandler(404)
def not_found(e):
    """Custom 404 handler with helpful message"""
    return render_template('index.html', 
                         Breadcrumb=[], 
                         Entries=[]), 404

if __name__ == '__main__':
    print("Running in development mode. For production, use Gunicorn via Docker.")
    app.run(host='0.0.0.0', port=HTTP_PROXY_PORT, debug=False)