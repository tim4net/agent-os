FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /agent-os ./cmd/server/

FROM alpine:3.23
# git is required by the worktree scanner (GET /api/worktrees) and other host
# integrations; without it the endpoint degrades to 503 (issue #123).
RUN apk add --no-cache ca-certificates git
COPY --from=builder /agent-os /usr/local/bin/agent-os
EXPOSE 8080
ENTRYPOINT ["agent-os"]
