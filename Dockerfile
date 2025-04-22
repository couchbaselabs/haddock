FROM scratch

# non-root user and group ID
ARG USER_ID=1001
ARG GROUP_ID=1001

WORKDIR /app

# Copy application files 
COPY --chown=${USER_ID}:${GROUP_ID} dashboard /app/dashboard
COPY --chown=${USER_ID}:${GROUP_ID} templates/ /app/templates/
COPY --chown=${USER_ID}:${GROUP_ID} static/ /app/static/

# Switch to the non-root user 
USER ${USER_ID}:${GROUP_ID}

CMD ["/app/dashboard"]

