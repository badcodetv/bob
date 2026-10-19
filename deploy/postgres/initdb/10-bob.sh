#!/usr/bin/env bash
# Runs once, on the first start of an EMPTY data directory (the official image's rule), as the
# superuser. Creates Bob's login and database with the per-role guards the box has always applied.
# BOB_DB_PASSWORD is app_bob's password: the same one BOB_DATABASE_URL carries.
set -euo pipefail
: "${BOB_DB_PASSWORD:?BOB_DB_PASSWORD must be set for the first start of Postgres}"
psql -v ON_ERROR_STOP=1 -v pw="$BOB_DB_PASSWORD" --username "$POSTGRES_USER" --dbname postgres <<'SQL'
CREATE ROLE app_bob LOGIN PASSWORD :'pw';
CREATE DATABASE bob OWNER app_bob;
REVOKE CONNECT ON DATABASE bob FROM PUBLIC;
GRANT  CONNECT ON DATABASE bob TO app_bob;
ALTER ROLE app_bob SET statement_timeout = '300s';
ALTER ROLE app_bob SET work_mem = '128MB';
\c bob
-- Needs the superuser; Bob's own migrations then find it present (CREATE EXTENSION IF NOT EXISTS).
CREATE EXTENSION vector;
SQL
