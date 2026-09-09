FROM restic/restic:0.18.1 AS restic

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY services/api/go.mod services/api/go.sum ./
RUN go mod download
COPY services/api .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/backup ./cmd/backup

FROM postgres:18-alpine AS backup
ARG VERSION=dev
LABEL org.opencontainers.image.title="Mailflow backup" \
      org.opencontainers.image.version="${VERSION}"
RUN apk add --no-cache ca-certificates tzdata
COPY --from=restic /usr/bin/restic /usr/local/bin/restic
COPY --from=build /out/backup /usr/local/bin/mailflow-backup
ENTRYPOINT ["/usr/local/bin/mailflow-backup"]
CMD ["daemon"]
