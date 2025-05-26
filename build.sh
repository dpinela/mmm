#!/bin/sh
CGO_ENABLED=1 CC='zig cc -target x86_64-linux-musl' GOOS=linux GOARCH=amd64 go build ./cmd/isthmus
zip -m isthmus-linux-amd64.zip isthmus
CGO_ENABLED=1 CC='zig cc -target x86_64-windows' GOOS=windows GOARCH=amd64 go build ./cmd/isthmus
zip -m isthmus-windows-amd64.zip isthmus.exe
# TODO: add a working cross-compilation command for macOS
GOOS=darwin GOARCH=arm64 go build ./cmd/isthmus
zip -m isthmus-macos-arm64.zip isthmus