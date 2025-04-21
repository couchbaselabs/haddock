FROM scratch


WORKDIR /app

COPY --chown=0:0 dashboard /app/dashboard
COPY --chown=0:0 templates/ /app/templates/
COPY --chown=0:0 static/ /app/static/

CMD ["/app/dashboard"]

