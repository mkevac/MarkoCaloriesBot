# Git tags are the source of release versions, as in MarkoDownloadBot.
set shell := ["bash", "-uc"]

image_name := "mkevac/markocaloriesbot"
platforms := "linux/amd64,linux/arm64"

default:
    @just --list

# Build the Go binary.
build:
    CGO_ENABLED=0 go build -o markocaloriesbot .

# Run the existing regression tests with the race detector.
test:
    go test -race ./...

# Build a local image; tagged commits also receive a version tag.
docker:
    #!/usr/bin/env bash
    set -euo pipefail
    TAG=$(git describe --tags --exact-match --match 'v[0-9]*' 2>/dev/null || true)
    VERSION=dev
    TAGS=(-t {{image_name}}:latest)
    if [[ -n "$TAG" ]]; then
        if ! [[ "$TAG" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
            echo "ERROR: Expected a tag like v1.0.0; found $TAG" >&2
            exit 1
        fi
        if [[ -n "$(git status --porcelain)" ]]; then
            echo "ERROR: Commit or remove working-tree changes before building a versioned image." >&2
            exit 1
        fi
        VERSION=${TAG#v}
        TAGS+=(-t {{image_name}}:$VERSION)
    fi
    docker buildx build "${TAGS[@]}" --build-arg VERSION="$VERSION" \
        --build-arg REVISION="$(git rev-parse HEAD)" --load .

# Publish both architectures with version and latest tags from a clean release.
push:
    #!/usr/bin/env bash
    set -euo pipefail
    TAG=$(git describe --tags --exact-match --match 'v[0-9]*' 2>/dev/null || true)
    if ! [[ "$TAG" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
        echo "ERROR: Publishing requires a version tag on the current commit (e.g. v1.0.0)." >&2
        exit 1
    fi
    if [[ -n "$(git status --porcelain)" ]]; then
        echo "ERROR: Publishing requires a clean working tree." >&2
        exit 1
    fi
    VERSION=${TAG#v}
    docker buildx build --platform {{platforms}} \
        -t {{image_name}}:$VERSION -t {{image_name}}:latest \
        --build-arg VERSION="$VERSION" --build-arg REVISION="$(git rev-parse HEAD)" --push .

run:
    docker compose up -d

stop:
    docker compose down

# Increment the latest reachable minor version and tag the current commit.
bump:
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ -n "$(git status --porcelain)" ]]; then
        echo "ERROR: Commit working-tree changes before creating a release tag." >&2
        exit 1
    fi
    TAG=$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null || true)
    if ! [[ "$TAG" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
        echo "ERROR: No valid release tag found. Create the first one with: git tag v1.0.0" >&2
        exit 1
    fi
    MAJOR=${BASH_REMATCH[1]}
    MINOR=${BASH_REMATCH[2]}
    NEW_TAG="v${MAJOR}.$((MINOR + 1)).0"
    git tag "$NEW_TAG"
    echo "Created $NEW_TAG. Next: git push origin $NEW_TAG, then just push."

# Show the current commit tag and latest reachable release.
version:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "Current commit tag: $(git describe --tags --exact-match --match 'v[0-9]*' 2>/dev/null || echo none)"
    echo "Latest release: $(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null || echo none)"
    echo "Image: {{image_name}}"
