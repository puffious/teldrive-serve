# --- Stage 1: The Builder ---
# This stage installs dependencies into a virtual environment.
# We use the 'slim' version here because it's more compatible for building wheels if needed.
FROM python:3.11-slim as builder

# Set the working directory
WORKDIR /app

# Create a non-root user and a virtual environment that the user owns
RUN python -m venv /app/venv
ENV PATH="/app/venv/bin:$PATH"

# Copy only the requirements file to leverage Docker cache
COPY requirements.txt .

# Install dependencies into the virtual environment
# Using --no-cache-dir reduces layer size
RUN pip install --no-cache-dir -r requirements.txt


# --- Stage 2: The Final Image ---
# This stage builds the final, small, and secure image.
# We use 'alpine' as it's one of the smallest available base images.
FROM python:3.11-alpine

# Set the working directory
WORKDIR /app

# Create a non-root user to run the application for better security
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
USER appuser

# Copy the virtual environment with all installed packages from the builder stage
COPY --from=builder /app/venv ./venv

# Copy the application code
COPY . .

# Make sure the new venv is on the path
ENV PATH="/app/venv/bin:$PATH"

# Expose the port Gunicorn will run on
EXPOSE 8888

# Define the command to run the application using Gunicorn
# -w 4:  Starts 4 worker processes (a good starting point)
# -b 0.0.0.0:8888: Binds to all network interfaces on port 8888
# app:app: Tells Gunicorn to run the 'app' object from the 'app.py' file
CMD ["gunicorn", "-w", "4", "-b", "0.0.0.0:8888", "app:app"]