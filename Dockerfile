# syntax=docker/dockerfile:1

#############################
#  Build stage
#############################
FROM golang:1.24-alpine AS builder
WORKDIR /src

# Copy go.mod + download deps first = better layer caching
COPY go.mod ./
RUN go mod download

# Copy the rest of the source
COPY . .

# Produce a static, size-optimised binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags "-s -w" -o /out/epaxos .

#############################
#  Runtime stage
#############################
FROM alpine:3.20

# A place for config that’s easy to volume-mount over
WORKDIR /app
COPY --from=builder /out/epaxos              /usr/local/bin/epaxos
COPY peers.txt                               /etc/epaxos/peers.txt
COPY docker-entrypoint.sh                    /usr/local/bin/
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Use host network so each replica listens on the port in peers.txt
#  (No EXPOSE needed when --network host is used)
ENTRYPOINT ["docker-entrypoint.sh"]