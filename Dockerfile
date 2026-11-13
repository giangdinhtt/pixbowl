# --- build ---------------------------------------------------------------
FROM golang:1.27-alpine AS build

RUN apk add --no-cache build-base pkgconf vips-dev

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# cgo is required: govips talks to libvips directly.
RUN CGO_ENABLED=1 go build -trimpath -ldflags "-s -w" -o /out/pixbowl ./cmd/pixbowl

# --- runtime -------------------------------------------------------------
FROM alpine:3.22

RUN apk add --no-cache vips tini ca-certificates wget \
    && addgroup -g 10001 -S app \
    && adduser -u 10001 -S -G app app \
    && mkdir -p /data \
    && chown app:app /data

COPY --from=build /out/pixbowl /usr/local/bin/pixbowl

USER app
ENV PORT=8080 \
    HOST=0.0.0.0 \
    DATA_DIR=/data \
    LOG_LEVEL=info

EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/liveness || exit 1

ENTRYPOINT ["/sbin/tini", "--"]
CMD ["/usr/local/bin/pixbowl"]
