FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY shared/discovery ./shared/discovery
COPY cmd/discovery-collector ./cmd/discovery-collector
RUN CGO_ENABLED=0 go build -trimpath -o /collector ./cmd/discovery-collector
FROM alpine:3.24
RUN apk add --no-cache ca-certificates
COPY --from=build /collector /collector
USER 65532:65532
ENTRYPOINT ["/collector"]
