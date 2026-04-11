# --- STAGE 1: Go Builder ---
FROM golang:alpine AS go-builder
WORKDIR /app
RUN apk add --no-cache git

# Copy everything (including the style.css you just built)
COPY . .

# Build the Go binary
RUN go mod download
RUN go build -o main .

# --- STAGE 2: Final Image ---
FROM alpine:latest
WORKDIR /root/
RUN apk add --no-cache ca-certificates libc6-compat

# Copy the binary and static assets from the builder
COPY --from=go-builder /app/main .
COPY --from=go-builder /app/static ./static

# Since your .go files contain the templates, you don't need a views folder
# but if you have other assets, make sure they are copied.

EXPOSE 8080
CMD ["./main"]