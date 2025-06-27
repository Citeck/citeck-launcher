#!/bin/bash
set -e

# Check if a database name is provided
if [ -z "$1" ]; then
    echo "Usage: $0 <database_name>"
    exit 1
fi

export PGPASSWORD="$POSTGRESQL_PASSWORD"

# Database name and user name are the same
DB_NAME="$1"
DB_USER="$1"
DB_PASSWORD="$1"

# Check if the database exists
DB_EXISTS=$(psql -U postgres -tc "SELECT 1 FROM pg_database WHERE datname = '$DB_NAME';" | xargs)

if [[ $DB_EXISTS != "1" ]]; then
    echo "Database '$DB_NAME' does not exist. Creating database and user..."
    psql -U postgres -c "CREATE DATABASE \"$DB_NAME\";"
    psql -U postgres -c "CREATE USER \"$DB_USER\" WITH PASSWORD '$DB_PASSWORD';"
    psql -U postgres -c "GRANT ALL PRIVILEGES ON DATABASE \"$DB_NAME\" TO \"$DB_USER\";"
    echo "Database and user '$DB_NAME' created successfully."
else
    echo "Database '$DB_NAME' already exists. No action needed."
fi
