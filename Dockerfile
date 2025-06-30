# --- Stage 1: The Builder ---
# Use 'alpine' for the builder to match the final stage's environment.
FROM python:3.11-alpine as builder

# Add the C compiler and build tools needed to install gevent on Alpine.
RUN apk add --no-cache build-base

# Set the working directory
WORKDIR /app

# Create and activate a virtual environment
RUN python -m venv /app/venv
ENV PATH="/app/venv/bin:$PATH"

# Copy and install requirements
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt


# --- Stage 2: The Final Image ---
# Use the same 'alpine' base for the final image.
FROM python:3.11-alpine

# Set the working directory
WORKDIR /app

# Create a non-root user for security
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
USER appuser

# Copy the virtual environment, which was compiled in a compatible Alpine environment.
COPY --from=builder /app/venv ./venv

# Copy the application code
COPY . .

# Set the PATH to include the venv
ENV PATH="/app/venv/bin:$PATH"

# Expose the port
EXPOSE 8888

# Run the application with Gunicorn using the correctly compiled gevent worker
CMD ["gunicorn", "-k", "gevent", "--timeout", "300", "-w", "4", "-b", "0.0.0.0:8888", "app:app"]