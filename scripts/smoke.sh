#!/usr/bin/env sh
set -eu

base_url="${BASE_URL:-http://localhost:8080}"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

health_status="$(curl --silent --show-error --output "$tmp_dir/health.json" --write-out '%{http_code}' "$base_url/healthz")"
test "$health_status" = "200"

first_status="$(curl --silent --show-error --output "$tmp_dir/first.json" --write-out '%{http_code}' \
  --header 'Content-Type: application/json' \
  --data '{"url":"https://example.com/path?source=smoke"}' \
  "$base_url/shorten")"
test "$first_status" = "201"

short_url="$(jq --raw-output '.short_url' "$tmp_dir/first.json")"
test -n "$short_url"
code="${short_url##*/}"

second_status="$(curl --silent --show-error --output "$tmp_dir/second.json" --write-out '%{http_code}' \
  --header 'Content-Type: application/json' \
  --data '{"url":"https://example.com/path?source=smoke"}' \
  "$base_url/shorten")"
test "$second_status" = "200"
test "$(jq --raw-output '.short_url' "$tmp_dir/second.json")" = "$short_url"

location="$(curl --silent --show-error --head "$base_url/$code" | tr -d '\r' | awk -F ': ' 'tolower($1) == "location" { print $2 }')"
test "$location" = "https://example.com/path?source=smoke"

invalid_status="$(curl --silent --show-error --output "$tmp_dir/invalid.json" --write-out '%{http_code}' \
  --header 'Content-Type: application/json' \
  --data '{"url":"http://127.0.0.1/private"}' \
  "$base_url/shorten")"
test "$invalid_status" = "400"

echo "Smoke test passed"
