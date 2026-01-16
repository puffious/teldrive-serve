import os
import requests
from requests.adapters import HTTPAdapter
from urllib3.util.retry import Retry
from dotenv import load_dotenv
from flask import Flask, Response, request, render_template, abort, redirect, url_for

# --- Configuration ---
load_dotenv()
TELDRIVE_URL = os.getenv("TELDRIVE_URL")
TELDRIVE_TOKEN = os.getenv("TELDRIVE_TOKEN")
if not TELDRIVE_URL or not TELDRIVE_TOKEN:
    raise ValueError("TELDRIVE_URL and TELDRIVE_TOKEN must be set in the .env file.")
TELDRIVE_API_URL = f"{TELDRIVE_URL.rstrip('/')}/api"
HTTP_PROXY_PORT = 8888
app = Flask(__name__)

# --- Session with connection pooling and keep-alive ---
def create_session():
    """Create a persistent session with connection pooling and retry logic"""
    session = requests.Session()
    
    # Configure retry strategy for transient failures
    retry_strategy = Retry(
        total=3,  # Retry up to 3 times
        backoff_factor=0.3,  # Wait 0.3s, 0.6s, 1.2s between retries
        status_forcelist=[429, 500, 502, 503, 504],
        allowed_methods=["HEAD", "GET", "OPTIONS"]
    )
    
    # Configure adapter with connection pooling and keep-alive
    adapter = HTTPAdapter(
        max_retries=retry_strategy,
        pool_connections=20,  # Number of connection pools
        pool_maxsize=20,      # Max connections per pool
        pool_block=False
    )
    
    session.mount("http://", adapter)
    session.mount("https://", adapter)
    
    # Set keep-alive headers
    session.headers.update({
        'Connection': 'keep-alive',
        'Keep-Alive': '300'
    })
    
    return session

# Create global session for connection reuse
session = create_session()

def get_teldrive_items(path):
    api_endpoint = f"{TELDRIVE_API_URL}/files"
    headers = {"Authorization": f"Bearer {TELDRIVE_TOKEN}"}
    params = {"path": path, "limit": 1000}
    try:
        response = session.get(api_endpoint, headers=headers, params=params, timeout=10)
        if response.status_code == 404:
            return []
        response.raise_for_status()
        return response.json().get("items", [])
    except requests.exceptions.RequestException as e:
        print(f"Error fetching from Teldrive API (Path: {path}): {e}")
        abort(502, description="Could not connect to the Teldrive backend.")

def get_teldrive_file_by_id(file_id):
    """Get a file directly by its ID from Teldrive API"""
    api_endpoint = f"{TELDRIVE_API_URL}/files/{file_id}"
    headers = {"Authorization": f"Bearer {TELDRIVE_TOKEN}"}
    
    try:
        response = session.get(api_endpoint, headers=headers, timeout=10)
        if response.status_code == 404:
            return None
        response.raise_for_status()
        return response.json()
    except requests.exceptions.RequestException as e:
        print(f"Error fetching file by ID from Teldrive API (ID: {file_id}): {e}")
        abort(502, description="Could not connect to the Teldrive backend.")

@app.route('/dl/<file_id>')
def direct_download(file_id):
    # Always force download for direct download links
    file_item = get_teldrive_file_by_id(file_id)
    if not file_item:
        abort(404, description="File not found")
    return stream_file(file_item, force_download=True)

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
    parent_dir = os.path.dirname(api_path)
    item_name = os.path.basename(clean_path)

    if clean_path:
        parent_items = get_teldrive_items(parent_dir)
        for item in parent_items:
            if item['name'] == item_name and item['type'] == 'file':
                # Redirect to the new direct download URL format
                return redirect(url_for('direct_download', file_id=item['id']))

    items = get_teldrive_items(api_path)
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
    High-performance streaming proxy with connection pooling and error resilience.
    Handles connection drops gracefully by using persistent sessions.
    """
    file_id = file_item['id']
    file_name = file_item['name']
    stream_url = f"{TELDRIVE_API_URL}/files/{file_id}/{file_name}"
    
    # Forward range and conditional headers for proper resumption support
    headers_to_forward = {}
    for header in ['Range', 'If-Range', 'If-None-Match', 'If-Modified-Since']:
        value = request.headers.get(header)
        if value:
            headers_to_forward[header] = value
    
    try:
        # Use persistent session with connection pooling
        # Timeout: 30s connect, 300s read (5 minutes per chunk)
        td_response = session.get(
            stream_url,
            headers=headers_to_forward,
            cookies={"access_token": TELDRIVE_TOKEN},
            stream=True,
            allow_redirects=True,
            timeout=(30, 300)  # Reasonable timeouts with retry logic
        )
        td_response.raise_for_status()
        
        # Build response headers - only forward essential headers
        response_headers = {}
        for key in ['Content-Type', 'Content-Length', 'Accept-Ranges', 
                    'Content-Range', 'ETag', 'Last-Modified', 'Cache-Control']:
            if key in td_response.headers:
                response_headers[key] = td_response.headers[key]
        
        # Set appropriate Content-Disposition
        disposition = 'attachment' if force_download else 'inline'
        response_headers['Content-Disposition'] = f'{disposition}; filename="{file_name}"'
        
        # Ensure Content-Type is set
        if 'Content-Type' not in response_headers:
            import mimetypes
            content_type = mimetypes.guess_type(file_name)[0] or 'application/octet-stream'
            response_headers['Content-Type'] = content_type
        
        # Critical for resume support
        if 'Accept-Ranges' not in response_headers:
            response_headers['Accept-Ranges'] = 'bytes'
        
        # Optimized streaming generator
        def stream_from_teldrive():
            try:
                # 512KB chunks - balance between speed and failure detection
                # Smaller than 1MB to detect connection issues faster
                chunk_size = 512 * 1024
                
                for chunk in td_response.iter_content(chunk_size=chunk_size):
                    if chunk:  # Filter out keep-alive chunks
                        yield chunk
            except Exception as e:
                # Log error but let the client handle it (they'll retry with Range)
                print(f"Stream interrupted for {file_id}: {e}")
            finally:
                # Always close the upstream connection
                td_response.close()
        
        # Return streaming response with direct passthrough
        return Response(
            stream_from_teldrive(),
            status=td_response.status_code,
            headers=response_headers,
            direct_passthrough=True
        )
        
    except requests.exceptions.RequestException as e:
        print(f"Error streaming from Teldrive (file_id: {file_id}): {e}")
        abort(502, description="Could not connect to the Teldrive backend.")

if __name__ == '__main__':
    print("Running in development mode. For production, use Gunicorn via Docker.")
    app.run(host='0.0.0.0', port=HTTP_PROXY_PORT, debug=False)