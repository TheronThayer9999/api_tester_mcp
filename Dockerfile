# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/apiprobe-mcp .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/apiprobe-mcp /usr/local/bin/apiprobe-mcp
ENV APIPROBE_TRANSPORT=http \
    APIPROBE_ADDR=":8931" \
    APIPROBE_PATH=/mcp
EXPOSE 8931
ENTRYPOINT ["apiprobe-mcp"]
