FROM golang:1.20.2-alpine3.17

ADD . /build
WORKDIR /build

RUN apk add --no-cache tzdata libgit2 libgit2-dev git gcc g++ pkgconfig
ENV TZ Europe/Kyiv

RUN CGO_ENABLED=1 GOOS=linux go build -a -ldflags="-linkmode external -extldflags '-static' -w -s" -o zoomrs -v ./cmd/service

EXPOSE 8080

CMD ["./zoomrs", "--config", "config/config.yml"]