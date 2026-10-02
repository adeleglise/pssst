# The compiler runs on the build host and cross-compiles, so building an amd64
# image from an arm64 workstation needs no emulation.
FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/pssst ./cmd/psp-exporter \
    && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/fake-psp ./cmd/fake-psp \
    && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/pssst-check ./cmd/pssst-check

FROM scratch AS fake-psp
COPY --from=build /out/fake-psp /fake-psp
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/fake-psp"]

FROM scratch AS exporter
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/pssst /pssst
COPY --from=build /out/pssst-check /pssst-check
USER 65532:65532
EXPOSE 9099
ENTRYPOINT ["/pssst"]
CMD ["-config", "/etc/pssst/config.yml"]

# Bundled images carry the repository's configuration, for deployments that
# build from their own fork: the inventory is versioned code, so a change ships
# as a commit rather than as a file edited on the host. deploy/compose.yml
# builds these.
FROM exporter AS exporter-bundled
COPY deploy/pssst.psp.yml /etc/pssst/config.yml

FROM quay.io/prometheus/blackbox-exporter:v0.28.0 AS blackbox-bundled
COPY deploy/blackbox/blackbox.yml /etc/blackbox/blackbox.yml
ENTRYPOINT ["/bin/blackbox_exporter"]
CMD ["--config.file=/etc/blackbox/blackbox.yml"]
