---
name: build-binary
description: 'Use when: building Go commands, validating builds, producing executables, or the user asks to build. In this repo, build the runnable binary with go build -o, not only a plain package build.'
argument-hint: '<command or package to build>'
---

# Build Binary

## When to Use
- The user asks to build, compile, validate a command, or produce an executable.
- You changed code under `cmd/` or any package used by a command binary.
- You need to confirm the runnable program still builds, not just that packages type-check.

## Procedure
1. Identify the command package being built, usually under `./cmd/<name>`.
2. Build an actual runnable binary with `go build -o <binary-name> ./cmd/<name>`.
3. On Windows, use the `.exe` suffix for the output binary.
4. Use a plain `go build ./...` or `go build ./cmd/<name>` only as an additional package check, not as the only build validation.

## Repo Examples
- Cameras app: `go build -o cameras.exe ./cmd/cameras`
- Replays app: `go build -o replays.exe ./cmd/replays`
- Discovery app: `go build -o discovery.exe ./cmd/discovery`

## Reporting
When reporting build validation, state the binary command that was run and whether it succeeded.