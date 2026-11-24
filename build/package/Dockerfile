# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags "-s -w \
      -X github.com/dantotech/monsieur-logger/internal/version.Version=${VERSION} \
      -X github.com/dantotech/monsieur-logger/internal/version.Commit=${COMMIT} \
      -X github.com/dantotech/monsieur-logger/internal/version.Date=${DATE}" \
    -o /out/monsieur-logger ./cmd/monsieur-logger

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.title="monsieur-logger" \
      org.opencontainers.image.description="JSON log generator for testing Fluent Bit log loss" \
      org.opencontainers.image.source="https://github.com/dantotech/monsieur-logger"
COPY --from=build /out/monsieur-logger /monsieur-logger
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/monsieur-logger"]
