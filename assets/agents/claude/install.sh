#!/bin/sh
set -e

# Scripts must run: the postinstall copies the native binary into place.
cd "$(dirname "$0")"
npm ci --no-audit --no-fund
ln -sf "$PWD/node_modules/.bin/claude" /usr/local/bin/claude
