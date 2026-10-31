# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY --from=web /src/web/dist/ ./internal/server/ui/
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags='-s -w' -o /out/alertmanager-ui ./cmd/alertmanager-ui

FROM gcr.io/distroless/static-debian13:nonroot
LABEL org.opencontainers.image.source="https://github.com/aai-labs/aai-alertmanager-ui" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.description="A focused interface for your existing Prometheus Alertmanager"
COPY --from=build /out/alertmanager-ui /alertmanager-ui
COPY LICENSE NOTICE THIRD_PARTY_NOTICES.md /licenses/
COPY design-system/*-OFL.txt /licenses/fonts/
USER 65532:65532
EXPOSE 8080
ENV LISTEN_ADDR=:8080
ENTRYPOINT ["/alertmanager-ui"]
