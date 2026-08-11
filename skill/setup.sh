#!/bin/bash
set -e
cd "$(dirname "$0")/bridge"
echo "Building Agent Watch bridge..."
go build -o bridge .
echo "Setup complete."
