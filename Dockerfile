FROM golang:1.23.2-alpine3.20 AS build

RUN go install -v github.com/K0ng2/f95-rss@v0.2.1

FROM alpine:3.20
RUN apk add --no-cache tzdata

COPY --from=build /go/bin/f95-rss /bin
CMD ["/bin/f95-rss"]
