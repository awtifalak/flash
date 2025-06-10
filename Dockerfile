FROM golang:1.23-alpine AS builder

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . ./

RUN go build -ldflags="-w -s" -o server ./cmd/server

FROM alpine:3.19

RUN addgroup -S appgroup && adduser -S appuser -G appgroup

USER appuser
WORKDIR /home/appuser

COPY --from=builder /app/server ./

EXPOSE 8080

CMD ["./server"]
