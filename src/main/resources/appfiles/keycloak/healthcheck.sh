#!/usr/bin/env bash

HOST=127.0.0.1
PORT=8080
ENDPOINT="/realms/master"

exec 3<>/dev/tcp/${HOST}/${PORT} || exit 1

echo -e "GET ${ENDPOINT} HTTP/1.0\r\nHost: ${HOST}\r\n\r\n" >&3

RESPONSE=$(cat <&3)

if echo "$RESPONSE" | grep -q "200 OK"; then
  exit 0
else
  exit 1
fi
