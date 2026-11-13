# Security policy

## Supported versions

Only the latest commit on `main` is supported. pixbowl is a small self-hosted
service with no maintained release branches.

## Reporting a vulnerability

Please **do not open a public issue** for a security problem.

Use GitHub's private vulnerability reporting instead:

<https://github.com/giangdinhtt/pixbowl/security/advisories/new>

Include:

- what you found and why it matters
- steps to reproduce (a `curl` command or request sequence is ideal)
- the commit you tested and any non-default configuration
- whether the issue needs authentication or a specific storage backend

You can expect an initial response within a few days. This is a side project,
so there is no SLA, but valid reports will be fixed and credited if you want.

## In scope

- path traversal, header injection, request smuggling
- authentication or authorization bypass (delete tokens, `ADMIN_TOKEN`, `BASIC_AUTH`)
- denial of service through the image pipeline (decompression bombs, unbounded variants)
- leaking EXIF/GPS or other metadata that should have been stripped
- serving user-controlled HTML or script from the image origin

## Known and by design

- **Images are public**: anyone with a link can view them. Link-only is the
  access model, not a bug.
- **Rate limits are per-process** and reset on restart. Running several
  replicas means one budget per replica; enforce limits at the proxy if that
  matters to you.
- **Basic auth is only as good as your TLS**: the credentials are base64 in the
  `Authorization` header, so terminate TLS at the reverse proxy.
- **`?w=` sizes are whitelisted** by default; opening `ALLOWED_SIZES` to any
  value shifts the abuse cost onto your disk and CPU.
