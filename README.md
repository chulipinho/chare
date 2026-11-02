# chare

A small, self-hosted Markdown library for reading documents across devices — phone, tablet, laptop, and the jailbroken Kindle in the drawer. It is a single Go binary with an embedded web UI and a versioned HTTP API. The filesystem is the source of truth: any folder of Markdown files is a valid library.

Point it at a directory, open it from another device, and read. Uploads, new documents, folders, renames, and deletes all happen from the web UI; the API exists for future apps and scripts.

## Features

- Browse, upload, create, rename, and delete documents from a browser.
- Rich reader with breadcrumbs, table of contents, font-size controls, and a code-wrap toggle that remembers your choice.
- JavaScript-free Simple Reader designed for the Kindle experimental browser.
- GitHub-flavored Markdown: tables, task lists, strikethrough, autolinks.
- Relative links and images inside documents are rewritten automatically.
- Raw view and download for every document.
- JSON API (`/api/v1`) for scripting or a future Kindle app.
- No database, no build step for the UI, no runtime dependencies.
- Atomic writes, path confinement, symlink rejection, and HTML sanitization.

## Quick start

Requirements: Go 1.23 or newer.

```sh
make run
# or:
go run ./cmd/chare -data ./data
```

Open <http://localhost:8080>. From another device on the same network, use the host machine's LAN address, for example `http://192.168.1.20:8080`.

The data directory is created if it does not exist. Its layout is just files and folders:

```
data/
├── plans/
│   ├── alpha.md
│   └── notes.txt
└── diagrams/
    └── flow.png
```

Build a static binary:

```sh
make build
./chare -addr :8080 -data /path/to/library
```

## Configuration

Every flag has an environment variable equivalent.

| Flag | Environment | Default | Description |
| --- | --- | --- | --- |
| `-addr` | `CHARE_ADDR` | `:8080` | Listen address (`host:port`). |
| `-data` | `CHARE_DATA_DIR` | `data` | Library root directory. |
| `-max-upload` | `CHARE_MAX_UPLOAD` | `26214400` (25 MiB) | Maximum size per uploaded file, in bytes. |

```sh
CHARE_ADDR=:9000 CHARE_DATA_DIR=~/documents ./chare
```

## Using the web UI

- `/browse/` — the library. Navigate folders, upload files (or drag them onto the page), paste a new document, create a folder, rename or delete entries. Folder deletion only works when the folder is empty.
- `/read/<path>` — rich reader. Includes a table of contents when the document has headings, `S`/`M`/`L` font-size links (`?size=sm|md|lg`), a `Wrap code` toggle that is remembered in the browser, and links to the Simple Reader and raw source.
- `/simple/<path>` — the Simple Reader. No JavaScript, no external stylesheet, large type, minimal layout. Long code lines wrap by default.
- `/raw/<path>` — the file exactly as stored.
- `/assets/<path>` — images referenced by documents.

The management page works without JavaScript. JavaScript is only used for drag-and-drop uploading, the delete confirmation dialog, and remembering the rich reader's code-wrap preference. New documents stay in the current folder (a name without an extension gets `.md`); renames keep the entry in its current folder and require a supported extension. Names may not contain slashes.

### Reading on a Kindle

Open:

```
http://<host>:8080/simple/plans/alpha.md
```

From the rich reader, use the “Simple reader” link. You can also bookmark a Simple Reader URL directly. If a document link points to `/read/`, changing `/read/` to `/simple/` in the address bar gives the Kindle view.

## Supported files

| Kind | Extensions | Behavior |
| --- | --- | --- |
| Document | `.md`, `.markdown`, `.txt` | Listed, readable, rendered as Markdown (plain text docs too). |
| Asset | `.png`, `.jpg`, `.jpeg`, `.gif`, `.webp` | Listed, served inline, usable in document images. |
| Everything else | — | Rejected with HTTP 415. SVG is intentionally not allowed. |

YAML frontmatter at the start of a document is stripped when rendering, but kept in the raw file. Names beginning with `.`, paths containing `.` or `..` segments, backslashes, and symlinks are rejected. Uploads and new documents never overwrite existing files; `PUT` with `?overwrite=true` is the only replace path. Writes are atomic.

## HTTP API

All endpoints live under `/api/v1`. Responses are JSON except when returning document or image content. There is no authentication; see [Security](#security).

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/health` | `{"status":"ok"}` |
| `GET` | `/api/v1/tree?path=plans` | List a directory. Omit `path` for the root. |
| `GET` | `/api/v1/files/{path}` | Raw file, or rendered HTML with `?format=html` or `Accept: text/html`. Add `?download=1` for an attachment. |
| `PUT` | `/api/v1/files/{path}` | Create or replace a file from the request body. `?overwrite=true` permits replacing; `201` on create, `200` on replace. |
| `PATCH` | `/api/v1/files/{path}` | Rename/move. Body: `{"to":"new/path.md"}`. Existing destinations return `409`. |
| `DELETE` | `/api/v1/files/{path}` | Permanently delete a file. Returns `204`. |
| `POST` | `/api/v1/upload?dir=plans` | Multipart upload. Field `files` may repeat; `dir` may also be a form field. |
| `POST` | `/api/v1/dirs/{path}` | Create a directory. Returns `201`. |
| `DELETE` | `/api/v1/dirs/{path}` | Delete an empty directory. Returns `204`. |

Path segments should be percent-encoded (for example `plans/My%20Plan.md`).

### Examples

```sh
BASE=http://localhost:8080

curl "$BASE/api/v1/health"

curl "$BASE/api/v1/tree?path=plans"

curl -X PUT --data-binary @plan.md \
  "$BASE/api/v1/files/plans/plan.md"

curl -X PUT --data-binary @plan.md \
  "$BASE/api/v1/files/plans/plan.md?overwrite=true"

curl "$BASE/api/v1/files/plans/plan.md"
curl -H 'Accept: text/html' "$BASE/api/v1/files/plans/plan.md"
curl "$BASE/api/v1/files/plans/plan.md?download=1"

curl -F 'files=@plan.md' -F 'files=@diagram.png' \
  "$BASE/api/v1/upload?dir=plans"

curl -X PATCH -d '{"to":"archive/plan.md"}' \
  "$BASE/api/v1/files/plans/plan.md"

curl -X DELETE "$BASE/api/v1/files/archive/plan.md"
curl -X POST "$BASE/api/v1/dirs/archive"
curl -X DELETE "$BASE/api/v1/dirs/archive"
```

### Response shapes

```json
{"name":"plan.md","path":"plans/plan.md","kind":"document","size":1834,"modTime":"2026-10-31T23:31:10.358295236-03:00"}
```

```json
{"path":"plans","entries":[{"name":"archive","path":"plans/archive","kind":"dir","size":0,"modTime":"..."}]}
```

A processed upload batch returns `200` with per-file results; one failure does not abort the batch:

```json
{"uploaded":[...],"errors":[{"name":"plan.md","error":"already exists"}]}
```

Errors use `{"error":"message","code":"snake_case"}` with these status codes:

| Status | Code | Meaning |
| --- | --- | --- |
| `403` | `invalid_path` / `forbidden` | Unsafe path, or cross-origin state-changing request. |
| `404` | `not_found` | Path or directory does not exist. |
| `409` | `exists` / `not_empty` | Name collision, or deleting a non-empty directory. |
| `413` | `too_large` | Body or uploaded file exceeds the limit. |
| `415` | `unsupported_type` | Extension is not a Document or Asset. |
| `400` | `is_dir` / `not_dir` / `bad_request` | Wrong kind of path or malformed request. |
| `500` | `internal` | Unexpected server error (details are logged, not returned). |

State-changing requests (`POST`, `PUT`, `PATCH`, `DELETE`) are rejected when a browser supplies a cross-site `Origin` or `Sec-Fetch-Site`. Requests with neither header, such as `curl`, are allowed.

## Deployment

### Docker

```sh
docker build -t chare .
mkdir -p data
docker run --rm -p 8080:8080 -v "$PWD/data:/data" chare
```

The container serves `/data` and exposes port 8080. The image includes a healthcheck against `/api/v1/health`.

### Long-running service

Run the binary under systemd, launchd, or your process supervisor of choice:

```ini
[Unit]
Description=chare
After=network.target

[Service]
ExecStart=/usr/local/bin/chare -addr :8080 -data /srv/chare
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

Because the library is plain files, back it up by copying or syncing the data directory. Files added or edited by other tools (scp, Syncthing, an editor) show up immediately.

## Security

chare is designed for a trusted home network and has **no authentication and no TLS**.

- Do not expose it to the public internet. Use a VPN such as Tailscale, or put an authenticating reverse proxy in front of it.
- Uploaded Markdown is sanitized before rendering; scripts, iframes, forms, SVG, event handlers, and `javascript:` URLs are stripped.
- All paths are confined to the data directory; `..`, hidden paths, and symlinks are rejected.
- Only Documents and raster Assets can be created; everything else returns `415`.
- Uploads are size-limited and written atomically. Deletion is permanent.

## Development

```sh
make test    # go test ./... -race
make fmt     # gofmt -w .
make vet     # go vet ./...
```

Layout:

```
cmd/chare/            entry point, flags, graceful shutdown
internal/library/     filesystem-as-truth: validation, listing, atomic writes
internal/render/      goldmark + bluemonday pipeline and link rewriting
internal/httpapi/     /api/v1 handlers
internal/web/         templates, static CSS, page handlers, form actions
docs/adr/             architectural decisions
GLOSSARY.md           project vocabulary
```

`GLOSSARY.md` defines the domain terms (Library, Document, Asset, Document Path, Listing, Rich Reader, Simple Reader). The ADRs record why the filesystem is the source of truth, why rendering is server-side, why there is no authentication, and why the UI uses Go templates instead of Angular.
