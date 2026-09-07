FROM golang:1.26-alpine AS build
WORKDIR /src
COPY services/api/go.mod services/api/go.sum ./
RUN go mod download
COPY services/api .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sentry-token ./cmd/sentry-token && \
    mkdir -p /out/cdn && chmod 0750 /out/cdn

FROM gcr.io/distroless/static-debian12:nonroot
USER root
COPY --from=build /out/api /usr/local/bin/api
COPY --from=build /out/worker /usr/local/bin/worker
COPY --from=build /out/sentry-token /usr/local/bin/sentry-token
COPY --from=build --chown=65532:65532 /out/cdn /data/cdn
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]
