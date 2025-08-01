import os
import requests
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

def get_teldrive_items(path):
    api_endpoint = f"{TELDRIVE_API_URL}/files"
    headers = {"Authorization": f"Bearer {TELDRIVE_TOKEN}"}
    params = {"path": path, "limit": 1000}
    try:
        response = requests.get(api_endpoint, headers=headers, params=params)
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
        response = requests.get(api_endpoint, headers=headers)
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
    file_id = file_item['id']
    file_name = file_item['name']
    stream_url = f"{TELDRIVE_API_URL}/files/{file_id}/{file_name}"
    
    # Better handling of range requests
    range_header = request.headers.get('Range', '')
    
    # Copy necessary request headers
    headers_to_forward = {
        'If-Range': request.headers.get('If-Range', ''),
        'If-None-Match': request.headers.get('If-None-Match', ''),
        'If-Modified-Since': request.headers.get('If-Modified-Since', '')
    }
    
    # Only add Range header if it's not empty
    if range_header:
        headers_to_forward['Range'] = range_header
    
    # Remove empty headers
    headers_to_forward = {k: v for k, v in headers_to_forward.items() if v}
    
    # Direct streaming approach
    try:
        # Stream with cookies for authentication
        # Increased timeouts for large files
        td_response = requests.get(
            stream_url,
            headers=headers_to_forward,
            cookies={"access_token": TELDRIVE_TOKEN},
            stream=True,
            allow_redirects=True,
            timeout=(10, 300)  # (connect timeout, read timeout) - increased for large files
        )
        td_response.raise_for_status()
        
        # Prepare response headers
        response_headers = {
            key: value for key, value in td_response.headers.items()
            if key.lower() in [
                'content-type', 'content-length', 'accept-ranges', 
                'content-range', 'etag', 'last-modified', 'cache-control'
            ]
        }
        
        # Set Content-Disposition based on force_download flag
        if force_download:
            response_headers['Content-Disposition'] = f'attachment; filename="{file_name}"'
        else:
            # Always use inline for better media player support
            response_headers['Content-Disposition'] = f'inline; filename="{file_name}"'
        
        # Set Content-Type if missing
        if 'content-type' not in map(str.lower, response_headers.keys()):
            # Guess based on extension
            import mimetypes
            content_type = mimetypes.guess_type(file_name)[0] or 'application/octet-stream'
            response_headers['Content-Type'] = content_type
        
        # Important for video players - make sure these headers are present
        if 'Accept-Ranges' not in response_headers:
            response_headers['Accept-Ranges'] = 'bytes'
        
        # Using an iterator to better handle connection issues with proper chunk handling
        def generate_content():
            try:
                # For streaming video, a smaller chunk size can be more responsive for seeking
                # but we need a buffer to avoid too many small reads
                buffer_size = 256 * 1024  # 256KB buffer
                
                # Use the raw socket connection with proper timeout management
                for chunk in td_response.raw.stream(buffer_size, decode_content=False):
                    if chunk:  # filter out keep-alive chunks
                        yield chunk
                        
            except (requests.exceptions.RequestException, 
                    requests.exceptions.ConnectionError, 
                    requests.exceptions.ChunkedEncodingError) as e:
                print(f"Connection error during streaming (file_id: {file_id}): {e}")
            except Exception as e:
                print(f"Unexpected error during streaming (file_id: {file_id}): {e}")
            finally:
                td_response.close()
        
        # Return the response with direct_passthrough for better performance
        return Response(
            generate_content(),
            status=td_response.status_code,
            headers=response_headers,
            direct_passthrough=True
        )
        
    except requests.exceptions.RequestException as e:
        print(f"Error streaming file from Teldrive (file_id: {file_id}): {e}")
        abort(502, description="Could not connect to the Teldrive backend.")

if __name__ == '__main__':
    print("Running in development mode. For production, use Gunicorn via Docker.")
    app.run(host='0.0.0.0', port=HTTP_PROXY_PORT, debug=False)