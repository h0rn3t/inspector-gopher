FROM golang:1.24-alpine AS build
WORKDIR /src
COPY demo/pprof-demo/main.go .
RUN go mod init demo 2>/dev/null; go mod tidy; go build -o /demo main.go

FROM alpine:3.20
RUN apk add --no-cache curl
COPY --from=build /demo /demo
EXPOSE 6060
ENTRYPOINT ["/demo"]
