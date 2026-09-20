FROM --platform=$BUILDPLATFORM golang:1.27.0 AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY api ./api
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/argocd-notifier ./cmd/argocd-notifier
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/slack-backend ./cmd/slack-backend

FROM gcr.io/distroless/static-debian12:nonroot AS aggregation-engine
COPY --from=builder /out/argocd-notifier /usr/local/bin/argocd-notifier
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/argocd-notifier"]

FROM gcr.io/distroless/static-debian12:nonroot AS slack-backend
COPY --from=builder /out/slack-backend /usr/local/bin/slack-backend
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/slack-backend"]
