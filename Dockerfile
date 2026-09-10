FROM node:22-alpine AS web-builder

WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27-alpine AS panel-builder

WORKDIR /src/panel
COPY panel/go.mod panel/go.sum ./
RUN go mod download
COPY panel/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/vps-panel ./cmd/panel

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && addgroup -S panel \
    && adduser -S -G panel -u 10001 panel \
    && mkdir -p /app/data /app/web \
    && chown -R panel:panel /app

WORKDIR /app
COPY --from=panel-builder /out/vps-panel /app/vps-panel
COPY --from=web-builder /src/web/dist/ /app/web/

ENV PANEL_LISTEN_ADDR=:8080 \
    PANEL_DATA_DIR=/app/data \
    PANEL_WEB_DIR=/app/web

USER panel
EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/app/vps-panel", "healthcheck"]

ENTRYPOINT ["/app/vps-panel"]
