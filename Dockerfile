FROM golang:1.27-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/hearth ./cmd/hearth

FROM alpine:3.22
RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 hearth \
    && mkdir -p /home/hearth/data \
    && chown -R hearth:hearth /home/hearth
WORKDIR /home/hearth
COPY --from=build /out/hearth /usr/local/bin/hearth
USER hearth
ENV HEARTH_ADDR=:8080 \
    HEARTH_DATA=/home/hearth/data/hearth.db \
    HEARTH_ENV=production
VOLUME ["/home/hearth/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s CMD wget -q -O- http://127.0.0.1:8080/healthz | grep -q ok
ENTRYPOINT ["hearth"]
