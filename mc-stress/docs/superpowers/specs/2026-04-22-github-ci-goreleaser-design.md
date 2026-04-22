# Design Spec: GitHub CI/CD with GoReleaser

## Purpose
Automate the build, linting, and release process for the `mc-stress` Go project using GitHub Actions and GoReleaser.

## Architecture
- **CI Provider:** GitHub Actions
- **Release Tool:** [GoReleaser](https://goreleaser.com/)
- **Triggers:** 
  - `push` to `main`: Runs linting and snapshot builds (no release created).
  - `tag` (pattern `v*`): Runs linting and creates a full GitHub Release.

## Components

### 1. GitHub Workflow (`.github/workflows/release.yml`)
- **Jobs:**
  - `lint`: Runs `go fmt`, `go vet`, and `staticcheck` (if possible).
  - `release`: 
    - Sets up Go environment.
    - Runs GoReleaser.
    - Uses `GITHUB_TOKEN` for permissions.

### 2. GoReleaser Config (`.goreleaser.yaml`)
- **Builds:**
  - Targets: `linux/amd64`, `linux/arm64`, `windows/amd64`, `darwin/amd64`, `darwin/arm64`.
  - Main: `./main.go`.
  - Flags: `-s -w` (strip debug info for smaller binaries).
- **Archives:**
  - Formats: `tar.gz` for Linux/macOS, `zip` for Windows.
  - Name template: `{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}`.
- **Checksum:**
  - Generates `checksums.txt` using SHA-256.
- **Release:**
  - Automated changelog generation from git commits.

## Testing Strategy
- **Snapshot builds:** Ensuring GoReleaser can build for all platforms on every push without creating a draft release.
- **Linting:** Ensuring code quality is maintained.

## Success Criteria
- Every push to `main` results in a successful build check.
- Every tag push (e.g., `v1.0.0`) automatically creates a GitHub Release with multi-platform binaries and checksums.
