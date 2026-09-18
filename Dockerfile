FROM --platform=$BUILDPLATFORM golang:1.27.0 AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/argocd-notifier ./cmd/argocd-notifier

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/argocd-notifier /usr/local/bin/argocd-notifier
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/argocd-notifier"]
