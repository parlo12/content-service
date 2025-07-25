#!/bin/sh
set -e

host="${DB_HOST}"
port="${DB_PORT:-5432}"
user="${DB_USER}"

echo "Waiting for Postgres at $host:$port (user: $user)..."

until pg_isready -h "$host" -p "$port" -U "$user"; do
  echo "Postgres is unavailable - sleeping"
  sleep 2
done

echo "✅ Postgres is ready. Starting service..."
exec "$@"