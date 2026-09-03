#!/bin/sh
set -eu

psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
    --set=app_password="$POSTGRES_APP_PASSWORD" \
    --set=database_name="$POSTGRES_DB" <<-'SQL'
\set ON_ERROR_STOP on

CREATE ROLE shortener_app
    LOGIN
    PASSWORD :'app_password'
    NOSUPERUSER
    NOCREATEDB
    NOCREATEROLE
    NOREPLICATION;

REVOKE CONNECT, TEMPORARY ON DATABASE :"database_name" FROM PUBLIC;
REVOKE ALL ON DATABASE :"database_name" FROM shortener_app;
GRANT CONNECT ON DATABASE :"database_name" TO shortener_app;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM shortener_app;
GRANT USAGE ON SCHEMA public TO shortener_app;
SQL
