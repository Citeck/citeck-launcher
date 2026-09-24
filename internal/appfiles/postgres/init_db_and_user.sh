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
    echo "Database '$DB_NAME' does not exist. Creating database and user..."
    # TEMPLATE template0, not the default template1: PostgreSQL refuses to copy
    # a template whose recorded collation version differs from the one the
    # image's C library provides ("template database \"template1\" has a
    # collation version mismatch"). The official images moved from Debian 12
    # (glibc 2.36) to Debian 13 (glibc 2.41), so every cluster initialized by an
    # older image meets that after an image update (seen on a live stand: data
    # at 2.36 under postgres:17.5-1.pgdg130). template0 is exempt from the
    # check, carries the same cluster defaults (encoding, locale), and holds
    # nothing a launcher database needs from template1.
    psql -U postgres -c "CREATE DATABASE \"$DB_NAME\" TEMPLATE template0;"
    psql -U postgres -c "CREATE USER \"$DB_USER\" WITH PASSWORD '$DB_PASSWORD';"
    psql -U postgres -c "GRANT ALL PRIVILEGES ON DATABASE \"$DB_NAME\" TO \"$DB_USER\";"
    psql -U postgres -d "$DB_NAME" -c "GRANT USAGE, CREATE ON SCHEMA public TO \"$DB_USER\";"
    psql -U postgres -d "$DB_NAME" -c "ALTER ROLE \"$DB_USER\" SET search_path TO public;"
    echo "Database and user '$DB_NAME' created successfully."
else
    echo "Database '$DB_NAME' already exists. No action needed."
fi
