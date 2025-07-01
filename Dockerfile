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

# --- CHANGE IS HERE ---
# Add --worker-connections to give a hint to gevent about the expected load.
# A value of 2000 is a safe, high number.
CMD ["gunicorn", "-k", "gevent", "--worker-connections", "2000", "--timeout", "300", "-w", "4", "-b", "0.0.0.0:8888", "app:app"]