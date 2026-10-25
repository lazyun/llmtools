#!/usr/bin/env bash
# Shared implementation; invoke one of build-macos/linux/windows.sh instead.

build_die() { printf 'Build error: %s\n' "$*" >&2; exit 1; }
build_require() { command -v "$1" >/dev/null 2>&1 || build_die "Missing command: $1. $2"; }
build_failed() { printf 'Build failed during %s (exit %s).\n' "$2" "$1" >&2; exit "$1"; }

build_main() {
    local build_platform="$1"
    shift
    local build_mcp_only=0 build_skip_install=0 build_stage=preflight
    local build_root build_host build_arch build_label build_suffix='' build_output
    local build_wails build_wails_version build_required_wails build_gopath build_candidate
    local build_mac_ldflags='' build_mcp build_app build_go_version build_major build_minor
    # Bash 3.2 (macOS) treats an empty array as unset with nounset enabled.
    local -a build_tags=(-tags '') build_wails_args=()

    case "$build_platform" in
        darwin) build_label=macos ;;
        linux) build_label=linux ;;
        windows) build_label=windows; build_suffix=.exe ;;
        *) build_die "Unknown platform: $build_platform" ;;
    esac
    while (($#)); do
        case "$1" in
            --mcp-only) build_mcp_only=1 ;;
            --skip-install) build_skip_install=1 ;;
            -h|--help)
                printf 'Usage: bash scripts/build-%s.sh [--mcp-only] [--skip-install]\n' "$build_label"
                printf '%s\n' 'Build on the matching operating system using its native Go/C toolchain.' \
                    '  --mcp-only      Build only ai-worker-mcp (no Node.js or Wails required).' \
                    '  --skip-install  Reuse existing frontend node_modules instead of npm ci.' \
                    'WAILS may specify the Wails CLI executable. Output: dist/<platform>-<arch>/.'
                return 0 ;;
            *) build_die "Unknown option: $1 (use --help)" ;;
        esac
        shift
    done

    build_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
    cd "$build_root"
    build_require go 'Install Go 1.26 or newer for the complete desktop build.'
    build_host="$(go env GOHOSTOS)"
    [[ "$build_host" == "$build_platform" ]] || build_die \
        "This script needs native $build_platform Go; detected $build_host. For Windows use Git Bash/MSYS2 with Windows Go, not WSL Go."
    build_arch="$(go env GOHOSTARCH)"
    case "$build_arch" in amd64|arm64) ;; *) build_die "Unsupported desktop architecture: $build_arch" ;; esac
    build_go_version="$(go env GOVERSION)"
    build_go_version="${build_go_version#go}"
    build_major="${build_go_version%%.*}"
    build_minor="${build_go_version#*.}"; build_minor="${build_minor%%.*}"
    [[ "$build_major" =~ ^[0-9]+$ && "$build_minor" =~ ^[0-9]+$ ]] || build_die 'Cannot determine the Go version.'
    if ((build_mcp_only)); then
        ((build_major > 1 || build_major == 1 && build_minor >= 25)) || build_die 'MCP requires Go 1.25 or newer.'
    else
        ((build_major > 1 || build_major == 1 && build_minor >= 26)) || build_die 'Desktop requires Go 1.26 or newer.'
        build_require node 'Install Node.js 22.19+.'
        build_require npm 'Install npm with Node.js.'
        if ((build_skip_install)); then
            [[ -d desktop/frontend/node_modules ]] || build_die '--skip-install requires desktop/frontend/node_modules; run without this option first.'
        fi
        build_required_wails="$(awk '$1 == "github.com/wailsapp/wails/v2" { print $2; exit } $1 == "require" && $2 == "github.com/wailsapp/wails/v2" { print $3; exit }' desktop/go.mod)"
        [[ -n "$build_required_wails" ]] || build_die 'Wails dependency is missing from desktop/go.mod.'
        build_wails="${WAILS:-wails}"
        if [[ "$build_platform" == windows ]] && command -v cygpath >/dev/null 2>&1 && [[ "$build_wails" == *\\* || "$build_wails" == ?:* ]]; then
            build_wails="$(cygpath -u "$build_wails")"
        fi
        if ! command -v "$build_wails" >/dev/null 2>&1; then
            [[ -z "${WAILS:-}" ]] || build_die "WAILS executable not found: $build_wails"
            build_gopath="$(go env GOPATH)"
            if [[ "$build_platform" == windows ]]; then
                build_gopath="${build_gopath%%;*}"
                if command -v cygpath >/dev/null 2>&1; then build_gopath="$(cygpath -u "$build_gopath")"; fi
            else
                build_gopath="${build_gopath%%:*}"
            fi
            build_candidate="$build_gopath/bin/wails$build_suffix"
            [[ -x "$build_candidate" ]] || build_die "Install Wails: go install github.com/wailsapp/wails/v2/cmd/wails@$build_required_wails (or set WAILS)."
            build_wails="$build_candidate"
        fi
        build_wails_version="$("$build_wails" version)"
        build_wails_version="${build_wails_version%%$'\n'*}"; build_wails_version="${build_wails_version%$'\r'}"
        [[ "$build_wails_version" == "$build_required_wails" ]] || build_die \
            "Wails CLI $build_required_wails required; found $build_wails_version. Run go install github.com/wailsapp/wails/v2/cmd/wails@$build_required_wails."

        case "$build_platform" in
            darwin)
                build_require xcode-select 'Install Xcode Command Line Tools.'
                xcode-select -p >/dev/null
                build_require codesign 'macOS signing tools are required.'
                build_require ditto 'macOS bundle copy tools are required.'
                build_mac_ldflags="${CGO_LDFLAGS:+$CGO_LDFLAGS }-framework UniformTypeIdentifiers"
                ;;
            linux)
                build_require pkg-config 'Install pkg-config, GTK 3 and WebKitGTK development packages.'
                pkg-config --exists gtk+-3.0 || build_die 'GTK 3 development files are missing (for example libgtk-3-dev).'
                if pkg-config --exists webkit2gtk-4.1; then
                    build_tags=(-tags webkit2_41)
                elif ! pkg-config --exists webkit2gtk-4.0; then
                    build_die 'WebKitGTK development files are missing (libwebkit2gtk-4.1-dev or libwebkit2gtk-4.0-dev).'
                fi
                ;;
        esac
    fi
    trap 'build_failed $? "$build_stage"' ERR
    build_output="$build_root/dist/$build_label-$build_arch"
    mkdir -p "$build_output" "$build_root/bin"
    build_mcp="$build_root/bin/ai-worker-mcp$build_suffix"

    if ((!build_mcp_only)); then
        if ((!build_skip_install)); then
            build_stage='frontend dependencies'
            npm --prefix "$build_root/desktop/frontend" ci
        fi
        build_stage='Wails bindings'
        mkdir -p "$build_root/desktop/frontend/dist"
        touch "$build_root/desktop/frontend/dist/.gitkeep"
        (
            cd "$build_root/desktop"
            if [[ "$build_platform" == darwin ]]; then
                CGO_ENABLED=1 GOOS="$build_platform" GOARCH="$build_arch" CGO_LDFLAGS="$build_mac_ldflags" "$build_wails" generate module "${build_tags[@]}"
            else
                CGO_ENABLED=1 GOOS="$build_platform" GOARCH="$build_arch" "$build_wails" generate module "${build_tags[@]}"
            fi
        )
        build_stage=frontend
        npm --prefix "$build_root/desktop/frontend" run build
    fi

    build_stage='CGO-enabled MCP'
    # SQLite requires CGO: never ship the CGO-disabled sqlite stub.
    CGO_ENABLED=1 GOOS="$build_platform" GOARCH="$build_arch" go build -trimpath -o "$build_mcp" ./cmd/ai-worker-mcp
    if ((build_mcp_only)); then
        cp "$build_mcp" "$build_output/ai-worker-mcp$build_suffix"
    else
        build_stage='desktop application'
        build_wails_args=(build -clean -s -m -skipbindings -nosyncgomod -trimpath "-platform=$build_platform/$build_arch")
        if [[ "$build_platform" == windows ]]; then build_wails_args+=(-webview2 download); fi
        (
            cd "$build_root/desktop"
            if [[ "$build_platform" == darwin ]]; then
                CGO_ENABLED=1 GOOS="$build_platform" GOARCH="$build_arch" CGO_LDFLAGS="$build_mac_ldflags" "$build_wails" "${build_wails_args[@]}" "${build_tags[@]}"
            else
                MSYS2_ARG_CONV_EXCL='-platform=' CGO_ENABLED=1 GOOS="$build_platform" GOARCH="$build_arch" "$build_wails" "${build_wails_args[@]}" "${build_tags[@]}"
            fi
        )
        build_stage=packaging
        if [[ "$build_platform" == darwin ]]; then
            build_app="$build_root/desktop/build/bin/AI Worker.app"
            [[ -f "$build_app/Contents/MacOS/AI Worker" ]] || build_die 'Wails did not produce AI Worker.app.'
            cp "$build_mcp" "$build_app/Contents/MacOS/ai-worker-mcp"
            codesign --force --deep --sign - "$build_app"
            codesign --verify --deep --strict "$build_app"
            # Only this fixed generated bundle is replaced; no custom output path is accepted.
            rm -rf -- "$build_output/AI Worker.app"
            ditto "$build_app" "$build_output/AI Worker.app"
            codesign --verify --deep --strict "$build_output/AI Worker.app"
        else
            build_app="$build_root/desktop/build/bin/AI Worker$build_suffix"
            [[ -f "$build_app" ]] || build_die "Wails did not produce $build_app."
            cp "$build_app" "$build_output/AI Worker$build_suffix"
            cp "$build_mcp" "$build_output/ai-worker-mcp$build_suffix"
        fi
    fi
    printf '\nBuild complete: %s\n' "$build_output"
}
