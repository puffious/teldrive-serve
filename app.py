import os
import requests
import threading
import mimetypes
import concurrent.futures
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
DISABLE_LOGS = os.getenv("DISABLE_LOGS", "false").lower() == "true"

if not TELDRIVE_URL or not TELDRIVE_TOKEN:
    raise ValueError("TELDRIVE_URL and TELDRIVE_TOKEN must be set in the .env file.")
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

# List cache and singleflight-like inflight map
list_cache = {}  # path -> {'ts': unix, 'items': [...]}
list_cache_ttl = 300
inflight = {}  # path -> threading.Event
inflight_lock = threading.Lock()

MAX_PAGE_WORKERS = 8
PAGE_LIMIT = 500
MAX_DOWNLOAD_WORKERS = 4
ACCEL_MIN_SIZE = 16 * 1024 * 1024  # 16 MiB

def fetch_page(path, page):
    api_endpoint = f"{TELDRIVE_API_URL}/files"
    headers = {"Authorization": f"Bearer {TELDRIVE_TOKEN}"}
    params = {"path": path, "limit": PAGE_LIMIT, "page": page}
    try:
        resp = session.get(api_endpoint, headers=headers, params=params, timeout=10)
        if resp.status_code == 404:
            return [], 404, None
        resp.raise_for_status()
        data = resp.json()
        items = data.get('items', [])
        meta = data.get('meta', {})
        return items, resp.status_code, meta
    except requests.exceptions.Timeout:
        log(f"Timeout fetching page {page} for {path}")
        abort(504, description="Teldrive backend timeout")
    except requests.exceptions.RequestException as e:
        log(f"Error fetching page {page} for {path}: {e}")
        abort(502, description="Could not connect to the Teldrive backend.")


def get_teldrive_items(path):
    """Paged list fetch with caching and singleflight-style dedupe.

    Returns (items, status_code).
    """
    # Normalize path - Teldrive expects leading slash for root vs empty
    api_path = path if path.startswith('/') else f"/{path}" if path else '/'

    # Check cache
    cached = list_cache.get(api_path)
    if cached and time() - cached['ts'] < list_cache_ttl:
        return cached['items'], 200

    # Singleflight: if another thread is fetching, wait
    with inflight_lock:
        ev = inflight.get(api_path)
        if ev is None:
            ev = threading.Event()
            inflight[api_path] = ev
            is_initiator = True
        else:
            is_initiator = False

    if not is_initiator:
        # wait for the inflight fetch to complete
        ev.wait(timeout=15)
        # attempt to return cached result
        cached = list_cache.get(api_path)
        if cached and time() - cached['ts'] < list_cache_ttl:
            return cached['items'], 200
        # fallback to a direct single fetch if cache wasn't populated

    try:
        # First page probe
        first_items, status_code, meta = fetch_page(api_path, 1)
        if status_code == 404:
            # clear event and return
            list_cache.pop(api_path, None)
            return [], 404

        total_pages = 1
        if meta and isinstance(meta.get('totalPages'), int):
            total_pages = meta.get('totalPages', 1)

        pages = [None] * total_pages
        pages[0] = first_items

        # Fetch remaining pages concurrently
        if total_pages > 1:
            def fetch_and_store(p):
                items, sc, _ = fetch_page(api_path, p)
                return p, items, sc

            with concurrent.futures.ThreadPoolExecutor(max_workers=min(MAX_PAGE_WORKERS, total_pages-1)) as ex:
                futures = {ex.submit(fetch_and_store, p): p for p in range(2, total_pages+1)}
                for fut in concurrent.futures.as_completed(futures):
                    try:
                        p, items, sc = fut.result()
                        pages[p-1] = items
                    except Exception as e:
                        log(f"Error fetching page for {api_path}: {e}")

        # Flatten pages preserving order
        all_items = []
        for pg in pages:
            if pg:
                all_items.extend(pg)

        # Cache
        list_cache[api_path] = {'ts': time(), 'items': all_items}
        return all_items, 200
    finally:
        # mark inflight done
        ev.set()
        with inflight_lock:
            inflight.pop(api_path, None)

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
    # Always proxy all downloads (no redirects or share logic).
    # Fetch metadata if available in cache; use minimal fallback otherwise.
    file_item = get_teldrive_file_by_id(file_id, use_cache=True)
    if not file_item:
        # Minimal fallback metadata when API metadata is missing
        file_item = {'id': file_id, 'name': file_id}
    # Auto-detect accelerated mode when upstream supports ranges and file is large
    size = file_item.get('size')
    if size is None:
        meta = get_teldrive_file_by_id(file_id, use_cache=True)
        if meta:
            size = meta.get('size')

    use_accel = False
    try:
        if size and size >= ACCEL_MIN_SIZE:
            # Probe upstream for range support via HEAD
            if TELDRIVE_TOKEN.startswith('access_token='):
                cookie_value = TELDRIVE_TOKEN
            else:
                cookie_value = f"access_token={TELDRIVE_TOKEN}"
            client_cookie = request.headers.get('Cookie')
            cookie_header = f"{client_cookie}; {cookie_value}" if client_cookie else cookie_value
            head = session.head(f"{TELDRIVE_API_URL}/files/{file_id}/{file_item.get('name', file_id)}",
                                headers={'Cookie': cookie_header}, allow_redirects=True, timeout=5)
            ar = head.headers.get('Accept-Ranges', '')
            if 'bytes' in ar.lower() or ('Content-Length' in head.headers and int(head.headers.get('Content-Length', 0)) == int(size)):
                use_accel = True
    except Exception:
        # Probe failed — fall back to normal streaming
        use_accel = False

    # Also allow explicit client override via query/header
    acc_q = request.args.get('acc')
    acc_h = request.headers.get('X-Accel')
    if (acc_q and acc_q in ('1', 'true', 'yes')) or (acc_h and acc_h == '1'):
        use_accel = True

    if use_accel:
        return accelerated_download(file_item)

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
    
    # Build upstream headers by starting with a whitelist of client headers
    # and then ensuring the Teldrive auth cookie is present (from TELDRIVE_TOKEN).
    allowed_request_headers = [
        'Range', 'If-Range', 'If-None-Match', 'If-Modified-Since',
        'User-Agent', 'Accept', 'Accept-Encoding', 'Accept-Language'
    ]
    upstream_headers = {}
    for h in allowed_request_headers:
        v = request.headers.get(h)
        if v:
            upstream_headers[h] = v

    # Ensure cookie auth is forwarded in Cookie header: support both raw token or prefixed value
    if TELDRIVE_TOKEN.startswith('access_token='):
        cookie_value = TELDRIVE_TOKEN
    else:
        cookie_value = f"access_token={TELDRIVE_TOKEN}"
    # Merge cookie header while preserving any client-sent cookies
    client_cookie = request.headers.get('Cookie')
    if client_cookie:
        upstream_headers['Cookie'] = f"{client_cookie}; {cookie_value}"
    else:
        upstream_headers['Cookie'] = cookie_value

    # Perform the upstream request with streaming and no read timeout
    try:
        td_response = session.request(
            method=request.method,
            url=stream_url,
            headers=upstream_headers,
            stream=True,
            allow_redirects=True,
            timeout=(10, None)
        )
    except requests.exceptions.RequestException as e:
        log(f"Error connecting to Teldrive for file {file_id}: {e}")
        abort(502, description="Could not connect to the Teldrive backend.")

    # Forward upstream status (200/206/etc.) and headers, filtering hop-by-hop headers
    hop_by_hop = {
        'connection', 'keep-alive', 'proxy-authenticate', 'proxy-authorization',
        'te', 'trailers', 'transfer-encoding', 'upgrade'
    }
    response_headers = {}
    for k, v in td_response.headers.items():
        if k.lower() in hop_by_hop:
            continue
        response_headers[k] = v

    # Override Content-Disposition based on force_download flag
    disposition = 'attachment' if force_download else 'inline'
    response_headers['Content-Disposition'] = f'{disposition}; filename="{file_name}"'

    # Ensure Accept-Ranges and Content-Type exist
    if 'Accept-Ranges' not in response_headers:
        response_headers['Accept-Ranges'] = 'bytes'
    if 'Content-Type' not in response_headers:
        import mimetypes
        response_headers['Content-Type'] = mimetypes.guess_type(file_name)[0] or 'application/octet-stream'

    # Stream generator with moderate chunk size to balance CPU and latency
    def stream_generator():
        chunk_size = 256 * 1024  # 256KB
        try:
            for chunk in td_response.iter_content(chunk_size=chunk_size):
                if chunk:
                    yield chunk
        except (requests.exceptions.ChunkedEncodingError,
                requests.exceptions.ConnectionError,
                ProtocolError,
                IncompleteRead) as e:
            log(f"Upstream stream interrupted for {file_id}: {e}")
            return
        except Exception as e:
            log(f"Unexpected streaming error for {file_id}: {e}")
            return
        finally:
            td_response.close()

    return Response(
        stream_generator(),
        status=td_response.status_code,
        headers=response_headers,
        direct_passthrough=True
    )


def accelerated_download(file_item, concurrency=MAX_DOWNLOAD_WORKERS):
    """Perform parallel ranged requests to upstream and stream concatenated result.

    Triggered via query `?acc=1` or header `X-Accel: 1` for clients that want a
    single-connection accelerated download. Falls back to normal streaming on error.
    """
    file_id = file_item['id']
    file_name = file_item.get('name', file_id)

    # Need file size
    size = file_item.get('size')
    if size is None:
        meta = get_teldrive_file_by_id(file_id, use_cache=True)
        if not meta or meta.get('size') is None:
            log(f"No size available for accelerated download of {file_id}")
            return stream_file(file_item, force_download=True)
        size = meta.get('size')

    if size == 0:
        return stream_file(file_item, force_download=True)

    # Determine client-requested range (for resume support)
    client_range = request.headers.get('Range')
    req_start = 0
    req_end = size - 1
    if client_range:
        # Support single byte-range only, e.g. 'bytes=123-456'
        try:
            parts = client_range.split('=')
            if len(parts) == 2 and parts[0].strip() == 'bytes':
                r = parts[1].strip()
                if '-' in r:
                    s_str, e_str = r.split('-', 1)
                    if s_str:
                        req_start = int(s_str)
                    if e_str:
                        req_end = int(e_str)
        except Exception:
            # Malformed Range header -> fall back to full
            req_start = 0
            req_end = size - 1

    # Build per-part ranges within requested window
    part_size = max(1024 * 1024, (req_end - req_start + 1) // concurrency)
    ranges = []
    start = req_start
    while start <= req_end:
        end = min(req_end, start + part_size - 1)
        ranges.append((start, end))
        start = end + 1

    # Prepare upstream headers with auth cookie
    if TELDRIVE_TOKEN.startswith('access_token='):
        cookie_value = TELDRIVE_TOKEN
    else:
        cookie_value = f"access_token={TELDRIVE_TOKEN}"
    client_cookie = request.headers.get('Cookie')
    cookie_header = f"{client_cookie}; {cookie_value}" if client_cookie else cookie_value

    # Launch requests concurrently
    part_responses = [None] * len(ranges)
    try:
        with concurrent.futures.ThreadPoolExecutor(max_workers=min(concurrency, len(ranges))) as ex:
            futures = []
            for idx, (s, e) in enumerate(ranges):
                hdrs = {'Range': f'bytes={s}-{e}', 'Cookie': cookie_header}
                fut = ex.submit(session.get, f"{TELDRIVE_API_URL}/files/{file_id}/{file_name}",
                                 headers=hdrs, stream=True, timeout=(10, None))
                futures.append((idx, fut))

            # Collect responses in order and stream; stop on first failure to allow client resume
            def gen():
                try:
                    for idx, fut in futures:
                        resp = fut.result()
                        try:
                            resp.raise_for_status()
                        except Exception as e:
                            log(f"Part request failed for {file_id} part {idx}: {e}")
                            # Stop streaming further parts; client can resume with Range
                            return
                        for chunk in resp.iter_content(chunk_size=256 * 1024):
                            if chunk:
                                yield chunk
                        resp.close()
                except Exception as e:
                    log(f"Accelerated download failed for {file_id}: {e}")
                    return

            # Build response headers: if client requested a range, respond with 206 and Content-Range
            total_len = req_end - req_start + 1
            headers = {
                'Content-Disposition': f'attachment; filename="{file_name}"',
                'Accept-Ranges': 'bytes',
                'Content-Type': mimetypes.guess_type(file_name)[0] if 'mimetypes' in globals() else 'application/octet-stream'
            }
            if client_range:
                headers['Content-Range'] = f'bytes {req_start}-{req_end}/{size}'
                headers['Content-Length'] = str(total_len)
                return Response(gen(), status=206, headers=headers, direct_passthrough=True)
            else:
                headers['Content-Length'] = str(size)
                return Response(gen(), status=200, headers=headers, direct_passthrough=True)
    except Exception as e:
        log(f"Unexpected error starting accelerated download for {file_id}: {e}")
        return stream_file(file_item, force_download=True)

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