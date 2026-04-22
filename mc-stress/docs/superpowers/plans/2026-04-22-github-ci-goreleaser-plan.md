# GitHub CI/CD with GoReleaser Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Automate building, linting, and releasing the `mc-stress` project using GitHub Actions and GoReleaser.

**Architecture:** A GitHub Actions workflow will handle linting and run GoReleaser. GoReleaser will manage cross-compilation and publishing to GitHub Releases on tag pushes.

**Tech Stack:** Go, GitHub Actions, GoReleaser.

---

### Task 1: Scaffolding GitHub Workflow

**Files:**
- Create: `.github/workflows/release.yml`

- [x] **Step 1: Create the workflow file**

```yaml
name: release

on:
  push:
    branches:
      - main
    tags:
      - 'v*'
  pull_request:

permissions:
  contents: write

jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.22'
          cache: true
      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@v6
        with:
          distribution: goreleaser
          version: latest
          args: release --clean --snapshot
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [x] **Step 2: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci: add initial github workflow for goreleaser"
```

---

### Task 2: GoReleaser Configuration

**Files:**
- Create: `.goreleaser.yaml`

- [x] **Step 1: Create GoReleaser config**

```yaml
version: 2

project_name: mc-stress

before:
  hooks:
    - go mod tidy

builds:
  - env:
      - CGO_ENABLED=0
    goos:
      - linux
      - windows
      - darwin
    goarch:
      - amd64
      - arm64
    main: ./main.go
    ldflags:
      - -s -w -X main.version={{.Version}} -X main.commit={{.Commit}} -X main.date={{.Date}}

archives:
  - format: tar.gz
    # use zip for windows archives
    format_overrides:
      - goos: windows
        format: zip
    name_template: >-
      {{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}

checksum:
  name_template: 'checksums.txt'

snapshot:
  name_template: "{{ .Tag }}-next"

changelog:
  sort: asc
  filters:
    exclude:
      - '^docs:'
      - '^test:'
```

- [x] **Step 2: Commit**

```bash
git add .goreleaser.yaml
git commit -m "ci: add goreleaser configuration"
```

---

### Task 3: Add Basic Unit Tests

**Files:**
- Create: `main_test.go`

- [x] **Step 1: Write tests for core logic**

```go
package main

import (
	"bytes"
	"testing"
)

func testWriteVarInt(t *testing.T) {
	tests := []struct {
		val      int
		expected []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7F}},
		{128, []byte{0x80, 0x01}},
		{255, []byte{0xFF, 0x01}},
		{2097151, []byte{0xFF, 0xFF, 0x7F}},
	}

	for _, tc := range tests {
		var buf []byte
		buf = writeVarInt(buf, tc.val)
		if !bytes.Equal(buf, tc.expected) {
			t.Errorf("writeVarInt(%d) = %v; want %v", tc.val, buf, tc.expected)
		}
	}
}

func TestCoreLogic(t *testing.T) {
	t.Run("writeVarInt", testWriteVarInt)
}
```

- [x] **Step 2: Run tests to verify**

Run: `go test -v main_test.go main.go`
Expected: PASS

- [x] **Step 3: Commit**

```bash
git add main_test.go
git commit -m "test: add basic unit tests for core logic"
```

---

### Task 4: Refine Workflow for Testing and Linting

**Files:**
- Modify: `.github/workflows/release.yml`

- [x] **Step 1: Add lint and test steps**

```yaml
name: release

on:
  push:
    branches:
      - main
    tags:
      - 'v*'
  pull_request:

permissions:
  contents: write

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4
      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.22'
          cache: true
      - name: Verify dependencies
        run: go mod verify
      - name: Build
        run: go build -v ./...
      - name: Test
        run: go test -v ./...

  goreleaser:
    needs: test
    runs-on: ubuntu-latest
    if: github.event_name == 'push' && (github.ref == 'refs/heads/main' || startsWith(github.ref, 'refs/tags/v'))
    steps:
      - name: Checkout
        uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.22'
          cache: true
      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@v6
        with:
          distribution: goreleaser
          version: latest
          args: >-
            {{ github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v') && 'release --clean' || 'release --clean --snapshot' }}
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [x] **Step 2: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci: update workflow to include tests and conditional release"
```
