# --- Stage 1: The Builder ---
    FROM golang:1.22-alpine AS builder

    WORKDIR /app
    
    COPY go.mod go.sum ./
    RUN go mod download
    
    COPY . .
    
    # Build a static, dependency-free binary
    RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /teldrive-proxy .
    
    # --- Stage 2: The Final, Minimal Image ---
    FROM alpine:latest
    
    # Add ca-certificates for any potential HTTPS needs
    RUN apk --no-cache add ca-certificates
    
    WORKDIR /app
    
    # Copy only the compiled binary and templates from the builder
    COPY --from=builder /teldrive-proxy .
    COPY --from=builder /app/templates ./templates
    
    EXPOSE 8888
    
    ENTRYPOINT ["./teldrive-proxy"]