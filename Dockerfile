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
    go build -trimpath -ldflags="-s -w" -o /out/aggregation-engine ./cmd/argocd-notifier
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/slack-backend ./cmd/slack-backend

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS aggregation-engine
COPY --from=builder /out/aggregation-engine /usr/local/bin/aggregation-engine
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/aggregation-engine"]

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS slack-backend
COPY --from=builder /out/slack-backend /usr/local/bin/slack-backend
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/slack-backend"]
