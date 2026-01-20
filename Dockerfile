# --- Stage 1: The Builder ---
FROM python:3.11-alpine as builder
RUN apk add --no-cache build-base
WORKDIR /app
RUN python -m venv /app/venv
ENV PATH="/app/venv/bin:$PATH"
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# --- Stage 2: The Final Image ---
FROM python:3.11-alpine
WORKDIR /app
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
USER appuser
COPY --from=builder /app/venv ./venv
COPY . .
ENV PATH="/app/venv/bin:$PATH"
EXPOSE 8888

# Use optimized gunicorn config for high-throughput streaming
CMD ["gunicorn", "-c", "gunicorn_config.py", "app:app"]