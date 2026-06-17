FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /agent-os ./cmd/server/

FROM alpine:3.23
RUN apk add --no-cache ca-certificates
COPY --from=builder /agent-os /usr/local/bin/agent-os
EXPOSE 8080
ENTRYPOINT ["agent-os"]
