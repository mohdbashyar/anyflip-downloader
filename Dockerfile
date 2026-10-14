FROM golang:alpine AS build

WORKDIR /app
COPY . .

ENV CGO_ENABLED=0
RUN go build -o anyflip-downloader .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
COPY --from=build /app/anyflip-downloader /usr/local/bin/anyflip-downloader

WORKDIR /data
ENV GOMEMLIMIT=350MiB
ENTRYPOINT ["anyflip-downloader"]
CMD ["-bot"]