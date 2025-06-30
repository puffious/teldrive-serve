import os
import requests
from dotenv import load_dotenv
from flask import Flask, Response, request, render_template, abort

# --- Configuration ---
load_dotenv()
TELDRIVE_URL = os.getenv("TELDRIVE_URL")
TELDRIVE_TOKEN = os.getenv("TELDRIVE_TOKEN")
if not TELDRIVE_URL or not TELDRIVE_TOKEN:
    raise ValueError("TELDRIVE_URL and TELDRIVE_TOKEN must be set in the .env file.")
TELDRIVE_API_URL = f"{TELDRIVE_URL.rstrip('/')}/api"
HTTP_PROXY_PORT = 8888
app = Flask(__name__)

# --- List of User-Agent substrings for media players ---
# These clients need `Content-Disposition: inline` to stream correctly.
MEDIA_PLAYER_AGENTS = [
    "VLC", "mpv", "LAVF", "Lavf", "ExoPlayer", "Kodi", "Plex", "IINA"
]

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

@app.route('/site.webmanifest')
@app.route('/favicon.ico')
def static_stubs():
    return '', 204

@app.route('/', defaults={'path': ''})
@app.route('/<path:path>')
def browse_and_download(path):
    clean_path = path.strip('/')
    api_path = f"/{clean_path}"
    parent_dir = os.path.dirname(api_path)
    item_name = os.path.basename(clean_path)

    if clean_path:
        parent_items = get_teldrive_items(parent_dir)
        for item in parent_items:
            if item['name'] == item_name and item['type'] == 'file':
                return stream_file(item)

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
        item_url = f"/{clean_path}/{item['name']}" if clean_path else f"/{item['name']}"
        is_dir = item['type'] == 'folder'
        if is_dir:
            item_url += '/'
        entries.append({
            'IsDir': is_dir,
            'URL': item_url,
            'Leaf': item['name'],
            'Size': item.get('size', 0)
        })
    entries.sort(key=lambda x: (not x['IsDir'], x['Leaf'].lower()))
    return render_template('index.html', Breadcrumb=breadcrumb, Entries=entries)

def stream_file(file_item):
    with requests.Session() as session:
        file_id = file_item['id']
        file_name = file_item['name']
        stream_url = f"{TELDRIVE_API_URL}/files/{file_id}/{file_name}"
        
        stream_headers = {"Range": request.headers.get("Range", "")}
        stream_cookies = {"access_token": TELDRIVE_TOKEN}

        session.headers.update(stream_headers)
        session.cookies.update(stream_cookies)

        try:
            td_response = session.get(stream_url, stream=True)
            td_response.raise_for_status()
        except requests.exceptions.RequestException as e:
            print(f"Error streaming file from Teldrive: {e}")
            abort(502, description="Could not stream file from the Teldrive backend.")

        def generate_content():
            try:
                for chunk in td_response.iter_content(chunk_size=8192):
                    yield chunk
            except Exception as e:
                print(f"Error during content generation: {e}")
            finally:
                td_response.close()
        
        response_headers = {
            key: value for key, value in td_response.headers.items()
            if key.lower() in [
                'content-type', 'content-length', 'accept-ranges', 
                'content-range', 'etag', 'last-modified'
            ]
        }
        
        # --- THE FINAL FIX IS HERE: USER-AGENT SNIFFING ---
        user_agent = request.headers.get('User-Agent', '')
        is_media_player = any(player in user_agent for player in MEDIA_PLAYER_AGENTS)
        
        if is_media_player:
            # For players like mpv, VLC, etc., allow inline streaming
            response_headers['Content-Disposition'] = f'inline; filename="{file_name}"'
        else:
            # For web browsers, force download for all files
            response_headers['Content-Disposition'] = f'attachment; filename="{file_name}"'
        
        return Response(generate_content(), status=td_response.status_code, headers=response_headers)

if __name__ == '__main__':
    print("Running in development mode. For production, use Gunicorn via Docker.")
    app.run(host='0.0.0.0', port=HTTP_PROXY_PORT, debug=False)