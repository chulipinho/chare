FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/chare ./cmd/chare

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /out/chare /usr/local/bin/chare
ENV CHARE_ADDR=:8080 \
    CHARE_DATA_DIR=/data
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- "http://127.0.0.1:${CHARE_ADDR##*:}/api/v1/health" || exit 1
ENTRYPOINT ["chare"]
