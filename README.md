# pixbowl

Image host tối giản cho VPS yếu: upload công khai, trả link riêng cho từng ảnh, resize bằng query string, chạy trên một binary Go duy nhất và **không cần database**.

- **Go + libvips** (qua `govips`) — cùng engine với sharp/imgproxy, nên resize/convert nhanh và tốn ít RAM.
- **Không DB** — metadata là `meta.json` nằm cạnh ảnh (hoặc object `meta.json` trên S3).
- **Local hoặc S3-compatible** — AWS S3, Cloudflare R2, MinIO, Backblaze B2.
- **Rate limit 3 tầng**, CORS chặn mặc định, xoá bằng token từng ảnh hoặc admin token.

## Tính năng

| Nhóm | Chi tiết |
|---|---|
| Upload | public, nhiều file/request, kéo–thả, dán clipboard, validate magic bytes |
| Xử lý ảnh | convert JPEG/PNG/WebP/AVIF, resize, auto-rotate, **strip EXIF/GPS** |
| Serve | link khó đoán (12 ký tự base62), cache immutable 1 năm, ETag/304, Range |
| Resize | `?w=320`, `?h=200`, `?w=640&h=640&fit=cover`, `?format=webp`, cache variant |
| Xoá | `X-Delete-Token` (người upload) hoặc `Authorization: Bearer <ADMIN_TOKEN>` |
| Chống lạm dụng | rate limit theo IP, giới hạn dung lượng/điểm ảnh, chặn SVG/HTML/PDF |
| Xác thực | HTTP Basic Auth tuỳ chọn cho trang upload; link ảnh vẫn công khai |
| Vận hành | `/liveness`, `/healthz`, log JSON, graceful shutdown, Docker non-root |
| Riêng tư | link-only (`noindex`), không gallery, không liệt kê thư mục |

## Chạy nhanh

### Docker (khuyến nghị)

```bash
cp .env.example .env
docker compose up -d --build
# mở http://localhost:8080
```

### Từ source

Cần Go 1.26+ **và** libvips (vì `govips` dùng cgo):

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
curl -F "file=@anh.jpg" https://pix.example.com/upload
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

Nhiều file trong một request (tối đa `MAX_FILES_PER_UPLOAD`):

```bash
curl -F "file=@a.jpg" -F "file=@b.png" https://pix.example.com/upload
# -> {"files":[...], "errors":[]}
```

Lỗi từng file được trả trong `errors`, các file hợp lệ vẫn upload bình thường.

### Lấy ảnh

```
GET /i/<id>.<ext>    bản đã tối ưu
GET /o/<id>.<ext>    bản gốc (chỉ khi KEEP_ORIGINAL=true)
```

Response có `Cache-Control: public, max-age=31536000, immutable`, `ETag`, `X-Robots-Tag: noindex`, `X-Content-Type-Options: nosniff`, hỗ trợ `If-None-Match` (304), `HEAD` và `Range`.

### Resize on-the-fly

```
/i/<id>.<ext>?w=320
/i/<id>.<ext>?h=200
/i/<id>.<ext>?w=640&h=640&fit=cover
/i/<id>.<ext>?w=200&format=webp
```

| Param | Giá trị | Mặc định |
|---|---|---|
| `w` | số nguyên, phải nằm trong `ALLOWED_SIZES` | — |
| `h` | số nguyên, cùng whitelist | — |
| `fit` | `inside` \| `cover` \| `contain` | `inside` |
| `format` | `webp` \| `jpeg` \| `png` \| `avif` | format của ảnh đang lưu |

- `inside`: vừa trong khung, giữ tỉ lệ, **không phóng to**. Chỉ truyền `w` thì chiều cao tự tính.
- `cover`: phủ kín khung rồi crop giữa, cho kết quả đúng kích thước (cho phép phóng to tối đa 2×).
- `contain`: vừa khung, phần trống bù nền trắng.
- Variant được sinh một lần rồi lưu tại `v/<key>` (local) hoặc `v/<key>` trên S3; request sau chỉ đọc file.
- Kích thước không nằm trong `ALLOWED_SIZES` trả `400` — đây là chốt chặn để kẻ xấu không spam `?w=1..9999` đốt CPU/ổ đĩa.
- Ảnh động (GIF/WebP animated) bỏ qua query và trả nguyên bản.

### Xoá

```bash
# người upload, dùng token nhận lúc upload
curl -X DELETE -H "X-Delete-Token: qaKZNgkfd5vn..." \
  https://pix.example.com/i/IwccWr3nkWvH.webp

# admin
curl -X DELETE -H "Authorization: Bearer $ADMIN_TOKEN" \
  https://pix.example.com/i/IwccWr3nkWvH.webp
```

Xoá luôn cả bản gốc và mọi variant. Token chỉ lưu dạng SHA-256. Sai token bị giới hạn riêng (`AUTH_ATTEMPTS_PER_MINUTE`) để không brute force được.

Trang `/d/<id>#<token>` cho phép xoá bằng trình duyệt; token nằm sau `#` nên không lọt vào log/referrer.

### Bảo vệ trang upload bằng Basic Auth

Chỉ cần **một biến** `BASIC_AUTH`, nhận 3 dạng:

```bash
# 1) user:password
BASIC_AUTH='alice:mat-khau-manh'

# 2) base64 của "user:password" — đúng chuỗi trong header Authorization: Basic
BASIC_AUTH="$(printf 'alice:mat-khau-manh' | base64)"

# 3) bcrypt hash — không lưu mật khẩu plaintext
BASIC_AUTH='alice:$2y$10$...'      # tạo bằng: htpasswd -nbB alice 'mat-khau-manh'
```

```bash
BASIC_AUTH='alice:mat-khau-manh' ./bin/pixbowl
```

Base64 chỉ là encoding, không phải mã hoá — ai đọc được biến môi trường thì giải mã được. Nếu muốn an toàn hơn khi file `.env` bị lộ, dùng dạng bcrypt (3).

```bash
curl -u alice:mat-khau-manh -F "file=@anh.jpg" https://pix.example.com/upload
```

Trình duyệt sẽ hỏi mật khẩu khi mở `/`. Chỉ **trang `/` và endpoint `POST /upload`** bị bảo vệ — `/i/`, `/o/`, `/d/<id>#token` và `/healthz` vẫn công khai, vì chặn link ảnh thì không còn chia sẻ được nữa. Sai mật khẩu bị tính chung hạn mức với token xoá (`AUTH_ATTEMPTS_PER_MINUTE`, mặc định 10 lần/phút/IP).

### Health

```
GET /liveness   200 ok, dùng cho Docker healthcheck
GET /healthz    kiểm tra storage thật, trả cả version libvips và format hỗ trợ
```

## Cấu hình

Toàn bộ qua biến môi trường, xem `.env.example`. Những biến quan trọng:

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `PORT` / `HOST` | `8080` / `0.0.0.0` | địa chỉ lắng nghe |
| `BASE_URL` | rỗng | origin công khai dùng trong URL trả về |
| `TRUST_PROXY` | `false` | bật khi chạy sau Caddy/nginx (đọc `X-Forwarded-For`) |
| `DATA_DIR` | `./data` | thư mục lưu ảnh (local) |
| `STORAGE` | `local` | `local` hoặc `s3` |
| `KEEP_ORIGINAL` | `true` | lưu cả bản gốc |
| `OUTPUT_FORMAT` | `preserve` | `preserve`/`jpeg`/`png`/`webp`/`avif` |
| `QUALITY` | `82` | chất lượng nén |
| `MAX_DIMENSION` | `2560` | cạnh dài tối đa của bản tối ưu, `0` = không resize |
| `STRIP_METADATA` | `true` | xoá EXIF/GPS |
| `MAX_FILE_MB` | `10` | dung lượng tối đa mỗi ảnh |
| `MAX_FILES_PER_UPLOAD` | `10` | số ảnh tối đa mỗi request |
| `MAX_PIXELS` | `40000000` | chặn ảnh quá lớn (bom nén) |
| `RATE_LIMIT_PER_MINUTE` / `_HOUR` / `_DAY` | `20` / `200` / `1000` | giới hạn upload theo IP, `0` = tắt |
| `AUTH_ATTEMPTS_PER_MINUTE` | `10` | giới hạn thử token xoá sai |
| `ALLOWED_ORIGINS` | rỗng | danh sách origin được CORS, rỗng = chặn hẳn |
| `ADMIN_TOKEN` | rỗng | token xoá mọi ảnh |
| `BASIC_AUTH` | rỗng | bảo vệ `/` và `POST /upload`; nhận `user:pass`, base64 hoặc bcrypt hash |
| `ALLOWED_SIZES` | `100,200,...,2560` | whitelist `?w=`/`?h=`, để trống = mọi giá trị 16..`MAX_VARIANT_DIMENSION` |
| `MAX_VARIANT_DIMENSION` | `4096` | chặn kích thước variant quá lớn |
| `VIPS_CONCURRENCY` | `1` | số thread libvips cho mỗi ảnh |
| `MAX_CONCURRENT_UPLOADS` | `2` | số upload xử lý song song |
| `MAX_CONCURRENT_VARIANTS` | `1` | số variant sinh song song |

S3: `S3_ENDPOINT_URL`, `S3_REGION`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, `S3_BUCKET_NAME`, `S3_PREFIX`, `S3_USE_SSL`.

## Dữ liệu lưu thế nào (không DB)

```
data/
  aB/
    aB3xK9mQ2pL7/
      image.webp        bản tối ưu  -> /i/
      original.jpg      bản gốc      -> /o/   (nếu KEEP_ORIGINAL)
      meta.json         id, created_at, sha256, kích thước, mime, hash token xoá
      v/
        w320-h0-inside-webp
        w640-h640-cover-webp
```

S3 dùng cùng layout: `<S3_PREFIX><id>/image.webp|meta.json|v/<key>`.

`image`/`original` được ghi trước, `meta.json` ghi sau cùng — request nào không thấy meta thì coi như 404, không bao giờ serve file dở dang. Backup = copy thư mục `data/` hoặc sync bucket.

## Triển khai

### Caddy

```
pix.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Đặt `TRUST_PROXY=true` và `BASE_URL=https://pix.example.com`, nếu không rate limit sẽ thấy mọi request đến từ `127.0.0.1`.

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

## Bảo mật

- **EXIF/GPS bị xoá** mặc định — ảnh điện thoại không làm lộ vị trí người upload.
- **Chỉ nhận ảnh thật**: kiểm tra magic bytes, chặn SVG/HTML/PDF (SVG là vector XSS), giới hạn `MAX_PIXELS` trước khi decode.
- **Không bao giờ serve HTML do người dùng kiểm soát**: ảnh trả với `nosniff`, trang HTML có CSP `default-src 'none'`.
- **ID 12 ký tự base62** (~71 bit) — không đoán được, không liệt kê được.
- **Token xoá lưu dạng hash**, so sánh constant-time, giới hạn số lần thử.
- **Upload có thể khoá bằng Basic Auth** (`BASIC_AUTH`, hỗ trợ bcrypt hash); link ảnh vẫn công khai theo đúng mô hình link-only.
- **Không có CORS mặc định** — JS ở site khác không đọc được ảnh; `<img>` vẫn nhúng bình thường.
- **AVIF phụ thuộc build libvips**. Nếu `heifsave` không có, server trả `400` cho `?format=avif` và từ chối khởi động với `OUTPUT_FORMAT=avif`. Kiểm tra `/healthz` để biết format hỗ trợ.

## Phát triển

```bash
make test     # go test ./... (cần libvips)
make vet
make build
```

```
cmd/pixbowl/         khởi động, graceful shutdown
internal/config/     đọc env, validate, defaults
internal/imageproc/  pipeline libvips + detect encoder (cgo)
internal/store/      interface + local + S3 (không DB)
internal/server/     routes, rate limit, auth, CORS, pages
internal/ratelimit/  fixed-window theo IP
web/                 trang upload và trang xoá (embed)
```

Test chạy thật: upload multipart, serve, ETag/304, resize, rate limit, CORS, xoá, path traversal.

## Giới hạn đã biết

- **Một instance**: rate limit và single-flight nằm trong RAM. Chạy nhiều replica cần rate limit ở tầng proxy (hoặc chấp nhận mỗi replica một hạn mức).
- **Ảnh động** được giữ nguyên byte gốc, không resize/convert — đổi lại không mất frame và không đốt CPU.
- **Không dedup theo hash**: cần index, mà index thì cần DB.
- **Không có gallery/account/dashboard** — đúng định hướng link-only.
