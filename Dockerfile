# Cross-compile PersonalRAG to any platform from a single Docker image.
#
# Self-contained: Go, Node.js, Wails CLI, Zig + macOS SDK, GTK/WebKit dev.
# The host needs only Docker – no Go, Node or wails3 required.
#
# Usage (from the repository root):
#   docker build -t wails-cross .          # or: wails3 task setup:docker
#   docker run --rm -v $(pwd):/app wails-cross darwin arm64
#   docker run --rm -v $(pwd):/app wails-cross darwin amd64
#   docker run --rm -v $(pwd):/app wails-cross linux amd64
#   docker run --rm -v $(pwd):/app wails-cross linux arm64
#   docker run --rm -v $(pwd):/app wails-cross windows amd64
#   docker run --rm -v $(pwd):/app wails-cross windows arm64
#
# Add -v wails-cross-gomod:/go/pkg/mod -v wails-cross-gocache:/root/.cache/go-build
# to keep Go modules and the build cache between runs.
#
# build.sh builds the frontend (npm ci → bindings → vite build) if
# frontend/dist is absent. For multi-platform runs, build it once on the host
# first so each Docker run only compiles the Go binary:
#   cd frontend && npm ci && cd ..
#   wails3 generate bindings -ts
#   cd frontend && npm run build && cd ..
# then run the container per platform.
#
# Linux targets link against GTK/WebKit of the image's own architecture, so
# an image built on an arm64 host builds linux/arm64 only (and vice versa).
# For the other one, build and run the image with --platform linux/<arch>.
#
# The macOS result is a bare, unsigned executable, not an .app bundle; a
# release still needs a Mac for bundling, signing and notarisation.

# Debian 13 provides the GTK >= 4.14 API baseline used by the default Linux backend.
FROM golang:1.26-trixie

ARG TARGETARCH
ARG WAILS_VERSION=v3.1.0
ARG GARBLE_VERSION=v0.16.0
ARG ZIG_VERSION=0.14.0
ARG MACOS_SDK_VERSION=14.5
ARG MACOS_SDK_SHA256=6e146275d19f027faa2e8354da5e0267513abf013b8f16ad65a231653a2b1c5d

# Install base tools, GCC, and GTK/WebKit dev packages (for Linux targets)
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl xz-utils python3 pkg-config gcc libc6-dev git nodejs npm \
    libgtk-3-dev libwebkit2gtk-4.1-dev \
    libgtk-4-dev libwebkitgtk-6.0-dev libx11-dev \
    && pkg-config --atleast-version=4.14 gtk4 \
    && pkg-config --exists webkitgtk-6.0 \
    && pkg-config --exists gtk+-3.0 \
    && pkg-config --exists webkit2gtk-4.1 \
    && rm -rf /var/lib/apt/lists/*

# Vite needs Node ^20.19 or >= 22.12; Debian 13 ships 20.19. Fail the image
# build loudly if that ever changes instead of failing every app build.
RUN node -e 'const [a,b]=process.versions.node.split(".").map(Number); if (!((a===20&&b>=19)||(a===22&&b>=12)||a>22)) { console.error("Node "+process.version+" too old for Vite"); process.exit(1) }' && \
    node --version && npm --version

# The repository is mounted from the host and owned by another user; let git
# read it (for the commit hash in the About box).
RUN git config --system --add safe.directory /app

# Wails CLI – needed for generate bindings, generate syso and icon tools
RUN go install github.com/wailsapp/wails/v3/cmd/wails3@${WAILS_VERSION}

# Garble for obfuscated builds (optional)
RUN go install mvdan.cc/garble@${GARBLE_VERSION}

# Install Zig - automatically selects correct binary for host architecture.
# The SHA-256 is fetched from Zig's official download manifest and verified
# before extraction to protect against MITM or corrupted downloads.
RUN ZIG_ARCH=$(case "${TARGETARCH}" in arm64) echo "aarch64" ;; *) echo "x86_64" ;; esac) && \
    ZIG_FILE="zig-linux-${ZIG_ARCH}-${ZIG_VERSION}.tar.xz" && \
    curl -fSL "https://ziglang.org/download/${ZIG_VERSION}/${ZIG_FILE}" -o "/tmp/${ZIG_FILE}" && \
    EXPECTED=$(curl -fsSL "https://ziglang.org/download/index.json" | \
        python3 -c "import json,sys; d=json.load(sys.stdin); print(d[sys.argv[1]][sys.argv[2]+'-linux']['shasum'])" \
        "${ZIG_VERSION}" "${ZIG_ARCH}") && \
    echo "${EXPECTED}  /tmp/${ZIG_FILE}" | sha256sum -c - && \
    tar -xJ -f "/tmp/${ZIG_FILE}" -C /opt && \
    rm "/tmp/${ZIG_FILE}" && \
    ln -s /opt/zig-linux-${ZIG_ARCH}-${ZIG_VERSION}/zig /usr/local/bin/zig

# Download macOS SDK (required for darwin targets)
RUN curl -fSL "https://github.com/joseluisq/macosx-sdks/releases/download/${MACOS_SDK_VERSION}/MacOSX${MACOS_SDK_VERSION}.sdk.tar.xz" -o /tmp/sdk.tar.xz \
    && echo "${MACOS_SDK_SHA256}  /tmp/sdk.tar.xz" | sha256sum -c - \
    && tar -xJ -f /tmp/sdk.tar.xz -C /opt \
    && rm /tmp/sdk.tar.xz \
    && mv /opt/MacOSX${MACOS_SDK_VERSION}.sdk /opt/macos-sdk

ENV MACOS_SDK_PATH=/opt/macos-sdk

# Create Zig CC wrappers for cross-compilation targets.
# Darwin and Windows use Zig; Linux uses native GCC (run with --platform for cross-arch)

# Darwin arm64
COPY <<'ZIGWRAP' /usr/local/bin/zcc-darwin-arm64
#!/bin/sh
ARGS=""
SKIP_NEXT=0
for arg in "$@"; do
    if [ $SKIP_NEXT -eq 1 ]; then
        SKIP_NEXT=0
        continue
    fi
    case "$arg" in
        -target) SKIP_NEXT=1 ;;
        -mmacosx-version-min=*) ;;
        *) ARGS="$ARGS $arg" ;;
    esac
done
exec zig cc -fno-sanitize=all -target aarch64-macos.13.0-none -isysroot /opt/macos-sdk -I/opt/macos-sdk/usr/include -L/opt/macos-sdk/usr/lib -F/opt/macos-sdk/System/Library/Frameworks -w $ARGS
ZIGWRAP
RUN chmod +x /usr/local/bin/zcc-darwin-arm64

# Darwin amd64
COPY <<'ZIGWRAP' /usr/local/bin/zcc-darwin-amd64
#!/bin/sh
ARGS=""
SKIP_NEXT=0
for arg in "$@"; do
    if [ $SKIP_NEXT -eq 1 ]; then
        SKIP_NEXT=0
        continue
    fi
    case "$arg" in
        -target) SKIP_NEXT=1 ;;
        -mmacosx-version-min=*) ;;
        *) ARGS="$ARGS $arg" ;;
    esac
done
exec zig cc -fno-sanitize=all -target x86_64-macos.13.0-none -isysroot /opt/macos-sdk -I/opt/macos-sdk/usr/include -L/opt/macos-sdk/usr/lib -F/opt/macos-sdk/System/Library/Frameworks -w $ARGS
ZIGWRAP
RUN chmod +x /usr/local/bin/zcc-darwin-amd64

# Windows amd64 - uses Zig's bundled mingw
COPY <<'ZIGWRAP' /usr/local/bin/zcc-windows-amd64
#!/bin/sh
ARGS=""
SKIP_NEXT=0
for arg in "$@"; do
    if [ $SKIP_NEXT -eq 1 ]; then
        SKIP_NEXT=0
        continue
    fi
    case "$arg" in
        -target) SKIP_NEXT=1 ;;
        -Wl,*) ;;
        *) ARGS="$ARGS $arg" ;;
    esac
done
exec zig cc -target x86_64-windows-gnu $ARGS
ZIGWRAP
RUN chmod +x /usr/local/bin/zcc-windows-amd64

# Windows arm64 - uses Zig's bundled mingw
COPY <<'ZIGWRAP' /usr/local/bin/zcc-windows-arm64
#!/bin/sh
ARGS=""
SKIP_NEXT=0
for arg in "$@"; do
    if [ $SKIP_NEXT -eq 1 ]; then
        SKIP_NEXT=0
        continue
    fi
    case "$arg" in
        -target) SKIP_NEXT=1 ;;
        -Wl,*) ;;
        *) ARGS="$ARGS $arg" ;;
    esac
done
exec zig cc -target aarch64-windows-gnu $ARGS
ZIGWRAP
RUN chmod +x /usr/local/bin/zcc-windows-arm64

# ---- Build script ----
# Runs the full PersonalRAG pipeline: frontend (if not pre-built), Windows
# syso (if Windows), then go build with the right C compiler and tags.
COPY <<'SCRIPT' /usr/local/bin/build.sh
#!/bin/sh
set -e

OS=${1:-darwin}
ARCH=${2:-arm64}

case "${OS}-${ARCH}" in
    darwin-arm64|darwin-aarch64)
        export CC=zcc-darwin-arm64
        export GOARCH=arm64 GOOS=darwin
        export CGO_ENABLED=1 CGO_CFLAGS="-w"
        ;;
    darwin-amd64|darwin-x86_64)
        export CC=zcc-darwin-amd64
        export GOARCH=amd64 GOOS=darwin
        export CGO_ENABLED=1 CGO_CFLAGS="-w"
        ;;
    linux-arm64|linux-aarch64)
        [ "$(uname -m)" = "aarch64" ] || { echo "linux/arm64 needs an arm64 image: docker build --platform linux/arm64 … and docker run --platform linux/arm64 …"; exit 1; }
        export CC=gcc
        export GOARCH=arm64 GOOS=linux
        export CGO_ENABLED=1 CGO_CFLAGS="-w"
        ;;
    linux-amd64|linux-x86_64)
        [ "$(uname -m)" = "x86_64" ] || { echo "linux/amd64 needs an amd64 image: docker build --platform linux/amd64 … and docker run --platform linux/amd64 …"; exit 1; }
        export CC=gcc
        export GOARCH=amd64 GOOS=linux
        export CGO_ENABLED=1 CGO_CFLAGS="-w"
        ;;
    windows-arm64|windows-aarch64)
        export GOARCH=arm64 GOOS=windows
        export CGO_ENABLED=0   # PersonalRAG is CGo-free on Windows (modernc.org/sqlite)
        ;;
    windows-amd64|windows-x86_64)
        export GOARCH=amd64 GOOS=windows
        export CGO_ENABLED=0   # PersonalRAG is CGo-free on Windows (modernc.org/sqlite)
        ;;
    *)
        echo "Usage: $0 <os> <arch>"
        echo "  os:   darwin, linux, windows"
        echo "  arch: amd64, arm64"
        exit 1
        ;;
esac

APP=${APP_NAME:-PersonalRAG}
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "")

# Files written into the mounted repository get its owner, not root.
OWNER=$(stat -c '%u:%g' /app)
trap 'chown -R "$OWNER" bin frontend/dist frontend/bindings 2>/dev/null || true' EXIT

# 1. Frontend: generate bindings, install deps, build – skipped if frontend/dist
#    already exists (host may have built it for multi-platform runs). npm runs
#    on a copy so the host's node_modules (with its own native binaries) stay
#    untouched.
if [ ! -d "frontend/dist" ]; then
    echo "==> Generating TypeScript bindings"
    wails3 generate bindings -ts -clean=true
    echo "==> Building frontend"
    rm -rf /tmp/frontend && mkdir /tmp/frontend
    tar -C frontend --exclude=node_modules --exclude=dist -cf - . | tar -C /tmp/frontend -xf -
    (cd /tmp/frontend && npm ci && npm run build)
    cp -R /tmp/frontend/dist frontend/dist
else
    echo "==> Frontend already built (frontend/dist exists), skipping"
fi

# 2. Windows: embed icon and manifest via .syso resource file
if [ "$GOOS" = "windows" ]; then
    echo "==> Generating Windows syso (${GOARCH})"
    wails3 generate syso -arch "$GOARCH" \
        -icon build/windows/icon.ico \
        -manifest build/windows/wails.exe.manifest \
        -info build/windows/info.json \
        -out "wails_windows_${GOARCH}.syso"
fi

# 3. Go build
EXT=""
LDFLAGS="-s -w"
[ -n "$COMMIT" ] && LDFLAGS="$LDFLAGS -X main.commit=$COMMIT"
if [ "$GOOS" = "windows" ]; then
    EXT=".exe"
    LDFLAGS="$LDFLAGS -H windowsgui"
fi

TAGS="production"
[ -n "$EXTRA_TAGS" ] && TAGS="$TAGS,$EXTRA_TAGS"

COMPILER="go build"
if [ "$OBFUSCATED" = "true" ]; then
    COMPILER="garble $GARBLE_ARGS build"
    TAGS="$TAGS,wails_obfuscated"
fi

echo "==> Building ${APP} for ${GOOS}/${GOARCH}"
mkdir -p bin
$COMPILER -tags "$TAGS" -trimpath -buildvcs=false \
    -ldflags="$LDFLAGS" \
    -o "bin/${APP}-${GOOS}-${GOARCH}${EXT}" .

# 4. Cleanup temporary syso
[ "$GOOS" = "windows" ] && rm -f *.syso

echo "==> Built: bin/${APP}-${GOOS}-${GOARCH}${EXT}"
SCRIPT
RUN chmod +x /usr/local/bin/build.sh

WORKDIR /app
ENTRYPOINT ["/usr/local/bin/build.sh"]
CMD ["darwin", "arm64"]
