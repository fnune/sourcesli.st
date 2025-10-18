FROM postgres:17-alpine

HEALTHCHECK --interval=5s --timeout=5s --retries=5 CMD pg_isready -U $POSTGRES_USER -d $POSTGRES_DB
