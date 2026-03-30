#!/bin/bash
set -e

# Check if a database name is provided
if [ -z "$1" ]; then
    echo "Usage: $0 <database_name>"
    exit 1
fi

export PGPASSWORD="$POSTGRES_PASSWORD"

# Database name and user name are the same
DB_NAME="$1"
DB_USER="$1"
DB_PASSWORD="$1"

# Check if the database exists
DB_EXISTS=$(psql -U postgres -tc "SELECT 1 FROM pg_database WHERE datname = '$DB_NAME';" | xargs)

if [[ $DB_EXISTS != "1" ]]; then
    echo "Creating database '$DB_NAME'..."
    psql -U postgres -c "CREATE DATABASE \"$DB_NAME\";"
fi

# Create user if not exists (idempotent — safe for migration from Kotlin)
USER_EXISTS=$(psql -U postgres -tc "SELECT 1 FROM pg_roles WHERE rolname = '$DB_USER';" | xargs)
if [[ $USER_EXISTS != "1" ]]; then
    echo "Creating user '$DB_USER'..."
    psql -U postgres -c "CREATE USER \"$DB_USER\" WITH PASSWORD '$DB_PASSWORD';"
fi

# Always ensure grants are correct
psql -U postgres -c "GRANT ALL PRIVILEGES ON DATABASE \"$DB_NAME\" TO \"$DB_USER\";" 2>/dev/null || true
psql -U postgres -d "$DB_NAME" -c "GRANT USAGE, CREATE ON SCHEMA public TO \"$DB_USER\";" 2>/dev/null || true
psql -U postgres -d "$DB_NAME" -c "ALTER ROLE \"$DB_USER\" SET search_path TO public;" 2>/dev/null || true

# Ensure password matches (Kotlin may have set a different one)
psql -U postgres -c "ALTER USER \"$DB_USER\" WITH PASSWORD '$DB_PASSWORD';" 2>/dev/null || true

echo "Database and user '$DB_NAME' ready."
