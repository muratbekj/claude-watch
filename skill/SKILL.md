---
name: claude-watch
description: Bridge your Claude Code session to the Agent Watch app on Apple Watch
author: shobhit
version: 0.1.0
---

# Agent Watch Bridge

Starts a local bridge server that connects your active Claude Code session
to the Agent Watch iOS/watchOS app.

## What it does
- Runs a Go bridge server on your LAN
- Registers HTTP hooks for real-time event forwarding
- Generates a 6-digit pairing code for the iPhone app
- Enables voice commands from your Apple Watch

## Usage
Run `/claude-watch` to start the bridge.
Enter the pairing code in the Agent Watch iPhone app.

## Setup
The bridge requires Go 1.22+ and the Unix `script` utility (present on macOS by default).
Run the setup script: `cd skill/bridge && go build -o bridge .`, then start it with `./bridge`
(or skip the build step and run `go run .` directly).
