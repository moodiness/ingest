FROM --platform=$BUILDPLATFORM node:22-alpine AS panel
WORKDIR /build/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26.8-alpine@sha256:8ac98ca534ac3f51e1f420a1dd2c15e74c75cfa0f23f3ad27eb5d7236c349a0c AS backend
WORKDIR /build
COPY go.mod go.sum ./
COPY torznab/ ./torznab/
RUN go mod download
COPY internal/ ./internal/
COPY cmd/ ./cmd/
COPY web/assets_embed.go ./web/assets_embed.go
COPY --from=panel /build/web/dist/ ./web/dist/
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -tags production -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /ingest ./cmd/ingest \
    && mkdir -p /runtime/data/providers \
    && chmod 700 /runtime/data /runtime/data/providers

FROM postgres:18.6-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873
# Ingest uses the PostgreSQL clients, not its root entrypoint or privilege helper.
RUN rm /usr/local/bin/gosu /usr/local/bin/docker-entrypoint.sh
COPY --from=backend /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=backend /ingest /ingest
COPY --from=backend --chown=65532:65532 /runtime/ /
ENV INGEST_DATA_DIR=/data \
    INGEST_PROVIDERS_DIR=/data/providers \
    INGEST_BIND=0.0.0.0:8080
WORKDIR /data
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/ingest"]
CMD ["serve"]
