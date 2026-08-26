#!/bin/sh
set -e

cd "$(dirname "$0")"
npm ci --ignore-scripts --no-audit --no-fund
ln -sf "$PWD/node_modules/.bin/pi" /usr/local/bin/pi
