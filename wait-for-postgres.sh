#!/bin/sh
set -e

host="${DB_HOST}"
port="${DB_PORT:-5432}"
user="${DB_USER}"
password="${DB_PASSWORD}"
dbname="${DB_NAME}"

export PGPASSWORD="$password"

echo "Waiting for Postgres at $host:$port (user: $user)..."

until psql "host=$host port=$port user=$user dbname=$dbname sslmode=require" -c '\q' > /dev/null 2>&1; do
  echo "Postgres is unavailable - sleeping"
  sleep 2
done

echo "✅ Postgres is ready. Starting service..."
exec "$@"