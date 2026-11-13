# pixbowl

A minimal image host for small VPSs: public uploads, one unguessable link per image, resize via query string, a single Go binary, and **no database**.

- **Go + libvips** (via `govips`) — the same engine behind sharp and imgproxy, so resizing and format conversion are fast and memory-friendly.
- **No database** — metadata is a `meta.json` file next to the image (or a `meta.json` object on S3).
- **Local or S3-compatible** — AWS S3, Cloudflare R2, MinIO, Backblaze B2.
- **Three-tier rate limiting**, CORS denied by default, per-image delete tokens or an admin token.

## Features

| Area | Details |
|---|---|
| Upload | public, multiple files per request, drag & drop, clipboard paste, magic-byte validation |
| Processing | convert JPEG/PNG/WebP/AVIF, resize, auto-rotate, **strip EXIF/GPS** |
| Serving | unguessable links (12 base62 chars), one-year immutable cache, ETag/304, Range |
| Resize | `?w=320`, `?h=200`, `?w=640&h=640&fit=cover`, `?format=webp`, cached variants |
| Delete | `X-Delete-Token` (uploader) or `Authorization: Bearer <ADMIN_TOKEN>` |
| Abuse control | per-IP rate limits, size/pixel caps, SVG/HTML/PDF rejected |
| Auth | optional Basic Auth for the upload page, plus `UPLOAD_TOKEN` for scripts; image links stay public |
| Operations | `/liveness`, `/healthz`, JSON logs, graceful shutdown, non-root Docker |
| Privacy | link-only (`noindex`), no gallery, no directory listing |

## Quick start

### Docker (recommended)

```bash
cp .env.example .env
docker compose up -d --build
# open http://localhost:8080
```

### From source

Requires Go 1.26+ **and** libvips (`govips` uses cgo):

```bash
# macOS
brew install go vips

# Debian/Ubuntu
sudo apt install libvips-dev

make build
./bin/pixbowl
```

## API

### Upload

```bash
curl -F "file=@photo.jpg" https://pix.example.com/upload
```

```json
{
  "id": "IwccWr3nkWvH",
  "url": "https://pix.example.com/i/IwccWr3nkWvH.webp",
  "original_url": "https://pix.example.com/o/IwccWr3nkWvH.jpg",
  "delete_url": "https://pix.example.com/d/IwccWr3nkWvH#qaKZNgkfd5vn...",
  "delete_token": "qaKZNgkfd5vn2zXuSPo1MlKYK4w1KtbY",
  "width": 1600,
  "height": 1200,
  "size": 41358,
  "format": "webp",
  "original_size": 155117
}
```

Multiple files in one request (up to `MAX_FILES_PER_UPLOAD`):

```bash
curl -F "file=@a.jpg" -F "file=@b.png" https://pix.example.com/upload
# -> {"files":[...], "errors":[]}
```

Per-file failures are returned in `errors`; the valid files are still stored.

### Serving

```
GET /i/<id>.<ext>    optimised copy
GET /o/<id>.<ext>    original    (only when KEEP_ORIGINAL=true)
```

Responses carry `Cache-Control: public, max-age=31536000, immutable`, `ETag`, `X-Robots-Tag: noindex` and `X-Content-Type-Options: nosniff`, and support `If-None-Match` (304), `HEAD` and `Range`.

### Resize on the fly

```
/i/<id>.<ext>?w=320
/i/<id>.<ext>?h=200
/i/<id>.<ext>?w=640&h=640&fit=cover
/i/<id>.<ext>?w=200&format=webp
```

| Param | Values | Default |
|---|---|---|
| `w` | integer, must be listed in `ALLOWED_SIZES` | — |
| `h` | integer, same whitelist | — |
| `fit` | `inside` \| `cover` \| `contain` | `inside` |
| `format` | `webp` \| `jpeg` \| `png` \| `avif` | format of the stored image |

- `inside`: fits the box, keeps the aspect ratio, **never enlarges**. With only `w`, the height is derived.
- `cover`: fills the box and centre-crops, always returning the exact size (may enlarge up to 2×).
- `contain`: fits the box and pads the rest with white.
- A variant is generated once and stored at `v/<key>` (local) or `v/<key>` on S3; later requests just read the file.
- Sizes outside `ALLOWED_SIZES` return `400` — this is what stops someone from spamming `?w=1..9999` to burn CPU and disk.
- Animated images (GIF/animated WebP) ignore the query and are served untouched.

### Delete

```bash
# uploader, using the token returned at upload time
curl -X DELETE -H "X-Delete-Token: qaKZNgkfd5vn..." \
  https://pix.example.com/i/IwccWr3nkWvH.webp

# admin
curl -X DELETE -H "Authorization: Bearer $ADMIN_TOKEN" \
  https://pix.example.com/i/IwccWr3nkWvH.webp
```

Deleting removes the original and every cached variant. Only the SHA-256 of the token is stored. Wrong tokens are rate limited separately (`AUTH_ATTEMPTS_PER_MINUTE`) so they cannot be brute-forced.

The `/d/<id>#<token>` page lets you delete from a browser. The token lives in the URL fragment, so it never reaches server logs or referrers.

### Protecting uploads with Basic Auth

`BASIC_AUTH` accepts **bcrypt hashes only** — the password itself never appears in your config:

```bash
# macOS ships /usr/sbin/htpasswd; on Debian/Ubuntu: apt install apache2-utils
BASIC_AUTH="$(htpasswd -nbB -C 10 alice 'strong-password')"
echo "$BASIC_AUTH"      # alice:$2y$10$...

BASIC_AUTH="$BASIC_AUTH" ./bin/pixbowl
```

Plain `user:password` and base64 forms are rejected at startup, so a typo cannot silently become a plaintext password. The hash must be exactly 60 characters with a cost of 14 or less (higher costs make every login take seconds).

```bash
curl -u alice:strong-password -F "file=@photo.jpg" https://pix.example.com/upload
```

Browsers prompt once when you open `/`. Only **the page `/` and the `POST /upload` endpoint** are guarded — `/i/`, `/o/`, `/d/<id>#token` and `/healthz` stay public, because protecting image links would defeat the point of a link-only host. Failed logins share the delete-token budget (`AUTH_ATTEMPTS_PER_MINUTE`, 10 per IP per minute by default).

### Upload tokens for scripts

`UPLOAD_TOKEN` is a bearer credential that only opens `POST /upload`. It cannot
delete, list, or read anything:

```bash
UPLOAD_TOKEN="$(openssl rand -hex 24)" ./bin/pixbowl

curl -H "Authorization: Bearer $UPLOAD_TOKEN" \
  -F "file=@photo.jpg" https://pix.example.com/upload
```

- Minimum 16 characters; the server refuses to start with a shorter one.
- Works alongside `BASIC_AUTH`: browsers keep using the password prompt, scripts
  use the token.
- With only `UPLOAD_TOKEN` set (no Basic Auth), the page stays public but asks
  for the token in a small field before uploading; it is kept in `localStorage`.
- Leaking it lets someone upload images (rate limits still apply) but never
  delete anything: `DELETE` still needs `X-Delete-Token` or `ADMIN_TOKEN`.

### Health

```
GET /liveness   200 ok, for Docker healthchecks
GET /healthz    checks the storage backend, returns the libvips version and supported formats
```

## Configuration

Everything is configured through environment variables; see `.env.example`. The important ones:

| Variable | Default | Meaning |
|---|---|---|
| `PORT` / `HOST` | `8080` / `0.0.0.0` | listen address |
| `BASE_URL` | empty | public origin used in returned URLs |
| `TRUST_PROXY` | `false` | enable behind Caddy/nginx (read `X-Forwarded-For`) |
| `DATA_DIR` | `./data` | image directory (local storage) |
| `STORAGE` | `local` | `local` or `s3` |
| `KEEP_ORIGINAL` | `true` | keep the untouched original |
| `OUTPUT_FORMAT` | `preserve` | `preserve`/`jpeg`/`png`/`webp`/`avif` |
| `QUALITY` | `82` | encoder quality |
| `MAX_DIMENSION` | `2560` | longest edge of the optimised copy, `0` = no resize |
| `STRIP_METADATA` | `true` | strip EXIF/GPS |
| `MAX_FILE_MB` | `10` | max size per image |
| `MAX_FILES_PER_UPLOAD` | `10` | max images per request |
| `MAX_PIXELS` | `40000000` | rejects huge images (decompression bombs) |
| `RATE_LIMIT_PER_MINUTE` / `_HOUR` / `_DAY` | `20` / `200` / `1000` | per-IP upload limits, `0` disables |
| `AUTH_ATTEMPTS_PER_MINUTE` | `10` | limit for wrong delete tokens and logins |
| `ALLOWED_ORIGINS` | empty | CORS allowlist; empty means no cross-origin access |
| `ADMIN_TOKEN` | empty | token that may delete any image |
| `UPLOAD_TOKEN` | empty | bearer token accepted by `POST /upload` only; min 16 chars |
| `BASIC_AUTH` | empty | guards `/` and `POST /upload`; accepts `user:<bcrypt hash>` only |
| `ALLOWED_SIZES` | `100,200,...,2560` | `?w=`/`?h=` whitelist; empty means any value from 16 to `MAX_VARIANT_DIMENSION` |
| `MAX_VARIANT_DIMENSION` | `4096` | hard cap on variant dimensions |
| `VIPS_CONCURRENCY` | `1` | libvips threads per image |
| `MAX_CONCURRENT_UPLOADS` | `2` | uploads processed in parallel |
| `UPLOAD_QUEUE_LIMIT` | `32` | requests allowed to wait for a worker |
| `MAX_CONCURRENT_VARIANTS` | `1` | variants generated in parallel |
| `ID_LENGTH` | `12` | base62 id length, 8–64 |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |

S3: `S3_ENDPOINT_URL`, `S3_REGION`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, `S3_BUCKET_NAME`, `S3_PREFIX`, `S3_USE_SSL`.

## Storage layout (no database)

```
data/
  aB/
    aB3xK9mQ2pL7/
      image.webp        optimised copy -> /i/
      original.jpg      original       -> /o/   (when KEEP_ORIGINAL)
      meta.json         id, created_at, sha256, size, mime, delete-token hash
      v/
        w320-h0-inside-webp
        w640-h640-cover-webp
```

S3 uses the same layout: `<S3_PREFIX><id>/image.webp|meta.json|v/<key>`.

`image`/`original` are written first and `meta.json` last — a request that finds no metadata gets a 404, so a half-written upload is never served. Backups are just a copy of `data/` or a bucket sync.

## Deployment

### Caddy

```
pix.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Set `TRUST_PROXY=true` and `BASE_URL=https://pix.example.com`, otherwise the rate limiter sees every request coming from `127.0.0.1`.

### nginx

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    client_max_body_size 12m;
}
```

### systemd

```ini
[Unit]
Description=pixbowl
After=network.target

[Service]
User=pixbowl
WorkingDirectory=/srv/pixbowl
EnvironmentFile=/srv/pixbowl/.env
ExecStart=/srv/pixbowl/bin/pixbowl
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

## Security

- **EXIF/GPS stripped by default** — phone photos do not leak the uploader's location.
- **Real images only**: magic-byte checks, SVG/HTML/PDF rejected (SVG is an XSS vector), `MAX_PIXELS` enforced before decoding.
- **Never serves user-controlled HTML**: images go out with `nosniff`, HTML pages carry a `default-src 'none'` CSP.
- **12-character base62 IDs** (~71 bits) — unguessable and unenumerable.
- **Delete tokens are stored hashed**, compared in constant time, and rate limited.
- **Uploads can be locked with Basic Auth** (`BASIC_AUTH`, bcrypt only) or an upload-only bearer token (`UPLOAD_TOKEN`); image links stay public, as a link-only host requires.
- **`UPLOAD_TOKEN` cannot delete**: a leaked script token can waste disk (rate limits still apply) but never remove someone else's image.
- **No CORS by default** — JavaScript on other sites cannot read images; plain `<img>` embedding still works.
- **AVIF depends on the libvips build**. Without `heifsave`, the server returns `400` for `?format=avif` and refuses to start with `OUTPUT_FORMAT=avif`. Check `/healthz` for the formats actually available.

## Development

```bash
make test     # go test ./... (needs libvips)
make vet
make build
```

```
cmd/pixbowl/         startup, graceful shutdown
internal/config/     env parsing, validation, defaults
internal/imageproc/  libvips pipeline + encoder detection (cgo)
internal/store/      interface + local + S3 (no DB)
internal/server/     routes, rate limiting, auth, CORS, pages
internal/ratelimit/  per-IP fixed windows
web/                 upload and delete pages (embedded)
```

The tests exercise the real thing: multipart upload, serving, ETag/304, resizing, rate limiting, CORS, deletion and path traversal.

## Known limitations

- **Single instance**: rate limits and variant single-flight live in memory. Running multiple replicas needs rate limiting at the proxy (or one budget per replica).
- **Animated images** are served byte-for-byte, never resized or converted — that keeps every frame and stays cheap on CPU.
- **No hash-based deduplication**: it needs an index, and an index needs a database.
- **No gallery, accounts or dashboard** — link-only by design.
