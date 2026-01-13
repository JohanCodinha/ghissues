FROM golang:alpine

# Install FUSE and dependencies
RUN apk add --no-cache fuse3 fuse3-dev git

# Allow Go to download newer toolchain if needed
ENV GOTOOLCHAIN=auto

# Enable FUSE for non-root (optional, we'll run as root for simplicity)
RUN echo "user_allow_other" >> /etc/fuse.conf

WORKDIR /app

# Copy go mod files first for caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build
RUN go build -o ghissues ./cmd/ghissues

# Create mount directory
RUN mkdir -p /mnt/issues

ENTRYPOINT ["/app/ghissues"]
