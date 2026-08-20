# syntax=docker/dockerfile:1

ARG GO_VERSION=1.24.5

FROM golang:${GO_VERSION} AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/yb-doctor \
    ./cmd/yb-doctor

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/yb-doctor /yb-doctor
USER nonroot:nonroot
ENTRYPOINT ["/yb-doctor"]
CMD ["--help"]
