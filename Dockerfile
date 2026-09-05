FROM golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/rowpress-server \
    ./cmd/rowpress-server

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/rowpress-server /rowpress-server
COPY THIRD_PARTY_NOTICES.md /THIRD_PARTY_NOTICES.md

USER 65532:65532
EXPOSE 10000

ENTRYPOINT ["/rowpress-server"]
