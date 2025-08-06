#!/bin/sh
CGO_ENABLED=1 CC='zig cc -target x86_64-linux-musl' GOOS=linux GOARCH=amd64 go build ./cmd/atoll && gzip -9 atoll