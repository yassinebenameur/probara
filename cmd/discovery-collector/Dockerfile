FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY shared/discovery ./shared/discovery
COPY cmd/discovery-collector ./cmd/discovery-collector
# Release version stamped into shared/buildinfo (docker-build.yml passes the
# metadata action's version). Declared just before the build so a new version
# only invalidates this layer.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-X github.com/yassinebenameur/probara/shared/buildinfo.Version=${VERSION}" -o /collector ./cmd/discovery-collector
FROM alpine:3.23
RUN apk add --no-cache ca-certificates
COPY --from=build /collector /collector
USER 65532:65532
ENTRYPOINT ["/collector"]
