FROM golang:1.26.3-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download && go mod verify 
COPY . .
RUN go build -o /gateway ./cmd/gateway/main.go


FROM alpine:latest
WORKDIR /app
COPY --from=builder /gateway /bin/gateway
EXPOSE 8080
CMD ["/bin/gateway"]