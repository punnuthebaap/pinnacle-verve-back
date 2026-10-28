# API image: static Go binaries (server, admin, seed) on a small Alpine base. Builds natively on
# arm64 (Oracle Ampere) and amd64. Migrations and fixtures are embedded in the binaries.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/server ./cmd/admin ./cmd/seed

FROM alpine:3.22
# wget (busybox) is used by the compose health check.
RUN addgroup -S app && adduser -S -G app app && mkdir -p /data/media && chown app:app /data/media
COPY --from=build /out/ /usr/local/bin/
USER app
ENV PORT=8080 MEDIA_DIR=/data/media
EXPOSE 8080
CMD ["server"]
