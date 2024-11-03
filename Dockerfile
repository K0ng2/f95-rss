FROM golang:1.23.2-alpine3.20 AS build
RUN apk add --no-cache git

WORKDIR /app
ENV CGO_ENABLED=1
RUN git clone https://github.com/K0ng2/f95-rss.git .
RUN go mod download

RUN go build -o /bin/f95-rss

FROM alpine:3.20
RUN apk add --no-cache tzdata
COPY --from=build /bin/f95-rss /bin
CMD ["/bin/f95-rss"]
