#!/bin/bash
# Build and bundle WarpLocal.app with Warp as the main app and the local adapter as a helper.
#
# Usage:
#   WARP_SRC=/path/to/warp-source ./build_and_bundle.sh [--launch]
#
# Prerequisites:
#   - Go toolchain
#   - Rust toolchain + Warp source (for building local Warp client)

set -euo pipefail
export PATH="$HOME/.cargo/bin:$HOME/go/bin:$PATH"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WARP_SRC="${WARP_SRC:-}"
# Keep the default release location, but allow a named preview bundle so UI work can be
# opened side-by-side without replacing the user's installed App.
BUNDLE_DIR="${WARPLOCAL_BUNDLE_DIR:-$SCRIPT_DIR/WarpLocal.app}"
ASSETS_DIR="$SCRIPT_DIR/assets"
GO_CACHE_DIR="$SCRIPT_DIR/.gocache"
GO_TMP_DIR="$SCRIPT_DIR/.gotmp"

if [[ -z "$WARP_SRC" ]]; then
  for candidate in \
    "$SCRIPT_DIR/../warp-v0.2026.04.29.08.56.stable_00-src/warp-0.2026.04.29.08.56.stable_00" \
    "$HOME/warp" \
    "$SCRIPT_DIR/../warp"
  do
    if [[ -f "$candidate/Cargo.toml" ]]; then
      WARP_SRC="$candidate"
      break
    fi
  done
fi

if [[ -z "$WARP_SRC" ]]; then
  echo "Warp source not found."
  echo "Set WARP_SRC to a local patched Warp source tree before running this script."
  echo "Example:"
  echo "  WARP_SRC=/path/to/warp-source ./build_and_bundle.sh"
  exit 1
fi

echo "Using WARP_SRC=$WARP_SRC"

NODE_BIN="${WARPLOCAL_NODE_BIN:-$(command -v node || true)}"
if [[ -z "$NODE_BIN" ]]; then
  echo "Node.js is required to bundle the Pi and DeepSeek Harness runtimes."
  exit 1
fi

NODE_VERSION="$($NODE_BIN -p 'process.versions.node' 2>/dev/null || true)"
NODE_MAJOR="${NODE_VERSION%%.*}"
NODE_MINOR="$(printf '%s' "$NODE_VERSION" | cut -d. -f2)"
if [[ ! "$NODE_MAJOR" =~ ^[0-9]+$ ]] || [[ ! "$NODE_MINOR" =~ ^[0-9]+$ ]] \
  || ! (( NODE_MAJOR >= 24 || (NODE_MAJOR == 22 && NODE_MINOR >= 19) )); then
  echo "Node.js ^22.19 or >=24 is required by every bundled Agent runtime; got ${NODE_VERSION:-unknown} from $NODE_BIN."
  echo "Set WARPLOCAL_NODE_BIN to a supported Node executable."
  exit 1
fi
echo "Using Node.js $NODE_VERSION from $NODE_BIN"

echo "=== Step 1/7: Building Agent runtime Sidecars ==="
for runtime_dir in "$SCRIPT_DIR/integrations/pi-agent" "$SCRIPT_DIR/integrations/deepseek-harness"; do
  echo "  -> $(basename "$runtime_dir")"
  (cd "$runtime_dir" && npm ci --silent && npm run build --silent)
done

echo ""
echo "=== Step 2/7: Running the cross-framework Agent contract matrix ==="
cd "$SCRIPT_DIR"
WARPLOCAL_CONTRACT_NODE="$NODE_BIN" go test ./cmd/server \
  -run '^(TestEveryBundledAgentHasContractRunner|TestAgentContractMatrix)$' -count=1

echo ""
echo "=== Step 3/7: Building warp-local-adapter (Go server) ==="
cd "$SCRIPT_DIR"
mkdir -p "$SCRIPT_DIR/bin" "$GO_CACHE_DIR" "$GO_TMP_DIR"
GOCACHE="$GO_CACHE_DIR" GOTMPDIR="$GO_TMP_DIR" GOFLAGS="-buildvcs=false" \
  go build -o "$SCRIPT_DIR/bin/warp-local-adapter" ./cmd/server
echo "  -> bin/warp-local-adapter"

echo ""
echo "=== Step 4/7: Building warp (WarpLocal client binary) ==="
cd "$WARP_SRC"
cargo build --bin warp --features skip_firebase_anonymous_user,ssh_drag_and_drop
echo "  -> target/debug/warp"

echo ""
echo "=== Step 5/7: Creating app bundle ==="
mkdir -p "$BUNDLE_DIR/Contents/MacOS"
mkdir -p "$BUNDLE_DIR/Contents/Helpers"
mkdir -p "$BUNDLE_DIR/Contents/Resources"
rm -f "$BUNDLE_DIR/Contents/MacOS/warplocal" "$BUNDLE_DIR/Contents/Helpers/warp-core" "$BUNDLE_DIR/Contents/Helpers/node-dsh" "$BUNDLE_DIR/Contents/Helpers/node-runtime"
rm -rf "$BUNDLE_DIR/Contents/Resources/pi-runtime" "$BUNDLE_DIR/Contents/Resources/dsh-runtime"

# Copy binaries
cp "$WARP_SRC/target/debug/warp" "$BUNDLE_DIR/Contents/MacOS/warp"
chmod +x "$BUNDLE_DIR/Contents/MacOS/warp"

cp "$SCRIPT_DIR/bin/warp-local-adapter" "$BUNDLE_DIR/Contents/Helpers/warp-local-adapter"
chmod +x "$BUNDLE_DIR/Contents/Helpers/warp-local-adapter"

cp "$NODE_BIN" "$BUNDLE_DIR/Contents/Helpers/node-runtime"
chmod +x "$BUNDLE_DIR/Contents/Helpers/node-runtime"

for runtime_name in pi-agent deepseek-harness; do
  source_dir="$SCRIPT_DIR/integrations/$runtime_name"
  if [[ "$runtime_name" == "pi-agent" ]]; then
    resource_name="pi-runtime"
  else
    resource_name="dsh-runtime"
  fi
  runtime_dest="$BUNDLE_DIR/Contents/Resources/$resource_name"
  mkdir -p "$runtime_dest"
  cp -R "$source_dir/dist" "$source_dir/node_modules" "$runtime_dest/"
  cp "$source_dir/package.json" "$source_dir/package-lock.json" "$runtime_dest/"
  if [[ -f "$source_dir/cordis.yml" ]]; then
    cp "$source_dir/cordis.yml" "$runtime_dest/cordis.yml"
  fi
done

# Copy example config
cp "$SCRIPT_DIR/config.example.yaml" "$BUNDLE_DIR/Contents/Resources/config.example.yaml"

# Copy diagnostics script
cp "$SCRIPT_DIR/diagnostics.sh" "$BUNDLE_DIR/Contents/Resources/diagnostics.sh"
chmod +x "$BUNDLE_DIR/Contents/Resources/diagnostics.sh"

# Copy icon if present
ICON_DEST="$BUNDLE_DIR/Contents/Resources/iconfile.icns"
if [[ -f "$ASSETS_DIR/iconfile.icns" ]]; then
  cp "$ASSETS_DIR/iconfile.icns" "$ICON_DEST"
elif [[ -f "$ASSETS_DIR/AppIcon.icns" ]]; then
  cp "$ASSETS_DIR/AppIcon.icns" "$ICON_DEST"
elif [[ -f "$WARP_SRC/app/channels/local/icon/no-padding/512x512.png" ]]; then
  cp "$WARP_SRC/app/channels/local/icon/no-padding/512x512.png" "$BUNDLE_DIR/Contents/Resources/warp-local-icon.png"
fi

# Write Info.plist
cat > "$BUNDLE_DIR/Contents/Info.plist" << 'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple Computer//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>English</string>
	<key>CFBundleDisplayName</key>
	<string>WarpLocal</string>
	<key>CFBundleExecutable</key>
	<string>warp</string>
	<key>CFBundleIdentifier</key>
	<string>dev.warp.Warp-Local</string>
	<key>CFBundleIconFile</key>
	<string>iconfile</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>WarpLocal</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>0.2.0</string>
	<key>CFBundleVersion</key>
	<string>0.2.0</string>
	<key>LSApplicationCategoryType</key>
	<string>public.app-category.developer-tools</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>CFBundleURLTypes</key>
	<array>
		<dict>
			<key>CFBundleURLName</key>
			<string>WarpLocal URL Scheme</string>
			<key>CFBundleURLSchemes</key>
			<array>
				<string>warplocal</string>
			</array>
		</dict>
	</array>
</dict>
</plist>
PLIST

echo ""
echo "=== Step 6/7: Signing app bundle ==="
SIGNING_IDENTITY="${WARPLOCAL_SIGNING_IDENTITY:-}"
if [[ -z "$SIGNING_IDENTITY" ]]; then
  SIGNING_IDENTITY="$(
    security find-identity -v -p codesigning 2>/dev/null \
      | awk '/Apple Development/ {print $2; exit}'
  )"
fi

if [[ -n "$SIGNING_IDENTITY" ]]; then
  echo "  -> stable Apple Development identity: $SIGNING_IDENTITY"
  codesign --force --deep --options runtime --sign "$SIGNING_IDENTITY" "$BUNDLE_DIR"
else
  echo "  -> no Apple Development identity found; using ad-hoc signature"
  codesign --force --deep --sign - "$BUNDLE_DIR"
fi
codesign --verify --deep --strict "$BUNDLE_DIR"

echo ""
echo "=== Step 7/7: Registering URL scheme ==="
LSREGISTER=$(find /System/Library/Frameworks/CoreServices.framework -name lsregister 2>/dev/null | head -1)
"$LSREGISTER" -f "$BUNDLE_DIR" 2>/dev/null || true

echo ""
echo "=== Done ==="
echo ""
echo "Bundle: $BUNDLE_DIR"
echo ""
echo "Contents:"
echo "  MacOS/warp               (WarpLocal main application)"
echo "  Helpers/warp-local-adapter (AI backend)"
echo "  Helpers/node-runtime       (Sidecar runtime)"
echo "  Resources/pi-runtime       (Pi Agent Sidecar)"
echo "  Resources/dsh-runtime      (DeepSeek Harness Sidecar)"
echo "  Resources/config.example.yaml"
echo ""
echo "To launch, run:"
echo "  open $BUNDLE_DIR"
echo ""

# Optionally launch
if [[ "${1:-}" == "--launch" ]]; then
    echo "Launching..."
    open "$BUNDLE_DIR"
fi
