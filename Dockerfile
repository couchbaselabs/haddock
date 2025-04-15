# Use the official Alpine Linux base image
FROM arm64v8/alpine:latest

# Set the working directory inside the container
WORKDIR /app

# Copy the dashboard binary from your build context into the container's working directory
# Make sure the 'dashboard' binary is in the same directory as this Dockerfile when you build
COPY dashboard /app/dashboard

# Copy templates and static folders to the app directory
COPY templates/ /app/templates/
COPY static/ /app/static/

# Make the binary executable
RUN chmod +x /app/dashboard

# Command to run the dashboard binary in a loop
# This ensures that if the binary exits (e.g., crashes), it will be restarted after a 1-second delay
CMD ["/bin/sh", "-c", "while true; do ./dashboard; echo 'dashboard crashed, restarting...'; sleep 1; done"]

#do nothing loop
#CMD ["/bin/sh", "-c", "while true; do sleep 1; done"]