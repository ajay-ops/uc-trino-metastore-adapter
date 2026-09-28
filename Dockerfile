# Pinned toolchain/base digest; review deliberately when upgrading Go/CA roots.
FROM golang:1.27.1-bookworm@sha256:69a7b9788769bec032d238959b61854e9ae87f57be9029ec04e9885fabf99195 AS build
WORKDIR /src
ENV GOTOOLCHAIN=local
# .dockerignore allowlists only build inputs, including optional committed vendor/.
COPY . .
ARG VERSION=dev
ARG GIT_SHA=unknown
ARG BUILD_TIMESTAMP=unknown
ARG SOURCE_DATE_EPOCH=0
RUN if [ -d vendor ]; then mode=vendor; else go mod download && go mod verify && mode=readonly; fi && \
    CGO_ENABLED=0 go build -mod="$mode" -buildvcs=false -trimpath \
    -ldflags="-s -w -buildid= -X github.com/ajay-ops/uc-trino-metastore-adapter/internal/observability.Version=${VERSION} -X github.com/ajay-ops/uc-trino-metastore-adapter/internal/observability.Revision=${GIT_SHA} -X github.com/ajay-ops/uc-trino-metastore-adapter/internal/observability.BuildTimestamp=${BUILD_TIMESTAMP}" \
    -o /out/uc-trino-metastore-adapter ./cmd/server

FROM scratch
ARG VERSION=dev
ARG GIT_SHA=unknown
ARG BUILD_TIMESTAMP=unknown
LABEL org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$GIT_SHA \
      org.opencontainers.image.created=$BUILD_TIMESTAMP
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/uc-trino-metastore-adapter /uc-trino-metastore-adapter
USER 65532:65532
EXPOSE 9083 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/uc-trino-metastore-adapter"]
