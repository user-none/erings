.PHONY: all clean desktop windows macos appimage icons dxil

# Output directories
BUILD_DIR := build
ICONSET_DIR := $(BUILD_DIR)/icon.iconset
APP_NAME := erings
APP_BUNDLE := $(BUILD_DIR)/$(APP_NAME).app
APPDIR := $(BUILD_DIR)/AppDir

# AppImage architecture. Defaults to the host machine's architecture so a local
# build produces a matching AppImage. CI overrides this (make ... APPIMAGE_ARCH=)
# to label cross-runner artifacts. The value follows the AppImage/linuxdeploy
# naming convention (x86_64, aarch64). The variables built from it below are
# recursively expanded, so uname runs only when the appimage target expands
# them and never on another platform's build.
APPIMAGE_ARCH ?= $(shell uname -m)

# Source files
ICON_MASTER := cmd/desktop/icon.webp
ICON_PNG := packaging/icon-512.png
ICON_ICNS := $(BUILD_DIR)/icon.icns
ICON_GRID := $(BUILD_DIR)/icon_grid.png
DESKTOP_FILE := packaging/erings.desktop

# AppImage tooling. linuxdeploy and its appimage plugin must be on PATH.
LINUXDEPLOY ?= linuxdeploy

# The DirectX Shader Compiler for the DX12 backend's compute shaders. dxil.dll
# must sit beside it so the compiled .dxil files are signed
DXC ?= dxc
DXIL_DIR := internal/hostgpu/dx12/dxil
HLSL_DIR := $(BUILD_DIR)/hlsl

# Version stamped into the binary with -ldflags. Supplied on the command line
# (make ... VERSION=...) so CI can stamp the same value it uses for artifact
# filenames. When empty (a plain local build or a source tarball) no version
# is injected and the binary resolves its version at runtime from the archive
# substitution or embedded build info.
VERSION ?=
ifneq ($(VERSION),)
VERSION_LDFLAGS := -X github.com/user-none/erings.Version=$(VERSION)
endif

# Update signing public keys stamped into the binary with -ldflags. Only the
# release workflow supplies them (make ... UPDATE_KEYS=...). When empty no keys
# are injected and the update system is off. The value only holds base64
# characters, ':' and ',' so it needs no quoting.
UPDATE_KEYS ?=
ifneq ($(UPDATE_KEYS),)
UPDATE_KEYS_LDFLAGS := -X github.com/user-none/erings.updateKeys=$(UPDATE_KEYS)
endif

# The macOS bundle's versions, stamped into its Info.plist from VERSION.
# CFBundleShortVersionString must be dotted integers, so it is VERSION when
# that is a release version (MAJOR.MINOR.PATCH), or 0.0.0 for a commit hash
# or no VERSION (the binary's own default). A commit hash can start with a
# digit, so the whole value is matched. It is recursively expanded, so the
# shell check runs only when the macos target uses it. CFBundleVersion
# carries VERSION as given.
BUNDLE_SHORT_VERSION = $(if $(shell echo '$(VERSION)' | grep -Ex '[0-9]+\.[0-9]+\.[0-9]+'),$(VERSION),0.0.0)
BUNDLE_VERSION := $(if $(VERSION),$(VERSION),0.0.0)

# The version segment is dropped when no VERSION is supplied (plain local builds).
ifneq ($(VERSION),)
APPIMAGE_NAME = $(APP_NAME)-$(VERSION)-$(APPIMAGE_ARCH).AppImage
else
APPIMAGE_NAME = $(APP_NAME)-$(APPIMAGE_ARCH).AppImage
endif
APPIMAGE = $(BUILD_DIR)/$(APPIMAGE_NAME)

# Build all targets
all: desktop

# Build the desktop binary
desktop:
	go build -ldflags "$(VERSION_LDFLAGS) $(UPDATE_KEYS_LDFLAGS)" -o $(BUILD_DIR)/erings ./cmd/desktop/

# Compile the compute packages to signed DXIL for the DX12 backend. The
# HLSL sources go under the build tree and the blobs into the directory
# every Windows build embeds; see $(DXIL_DIR)/README.md.
dxil:
	go run ./utils/dxil_gen -dxc "$(DXC)" -hlsl $(HLSL_DIR) -out $(DXIL_DIR)

# Build the Windows desktop binary as a GUI subsystem app so no console
# window is opened when it runs, with the dxil embedded.
windows: dxil
	go build -ldflags '-H=windowsgui -s -w -extldflags "-static -lpthread" $(VERSION_LDFLAGS) $(UPDATE_KEYS_LDFLAGS)' -o $(BUILD_DIR)/erings.exe ./cmd/desktop/

# Build a Linux AppImage.
appimage:
	go build -ldflags "-s -w $(VERSION_LDFLAGS) $(UPDATE_KEYS_LDFLAGS)" -o $(BUILD_DIR)/erings ./cmd/desktop/
	@rm -rf $(APPDIR)
	APPIMAGE_EXTRACT_AND_RUN=1 ARCH=$(APPIMAGE_ARCH) OUTPUT=$(APPIMAGE_NAME) $(LINUXDEPLOY) \
		--appdir $(APPDIR) \
		--executable $(BUILD_DIR)/erings \
		--desktop-file $(DESKTOP_FILE) \
		--icon-file $(ICON_PNG) \
		--icon-filename $(APP_NAME) \
		--output appimage
	@mv $(APPIMAGE_NAME) $(APPIMAGE)
	@echo "Created $(APPIMAGE)"

# Build macOS .app bundle.
macos: icons
	go build -ldflags "-s -w $(VERSION_LDFLAGS) $(UPDATE_KEYS_LDFLAGS)" -o $(BUILD_DIR)/erings ./cmd/desktop/
	@echo "Creating $(APP_NAME).app bundle..."
	@mkdir -p "$(APP_BUNDLE)/Contents/MacOS"
	@mkdir -p "$(APP_BUNDLE)/Contents/Resources"
	@cp $(BUILD_DIR)/erings "$(APP_BUNDLE)/Contents/MacOS/"
	@cp $(ICON_ICNS) "$(APP_BUNDLE)/Contents/Resources/icon.icns"
	@cp packaging/macos_info.plist "$(APP_BUNDLE)/Contents/Info.plist"
	@plutil -replace CFBundleShortVersionString -string "$(BUNDLE_SHORT_VERSION)" "$(APP_BUNDLE)/Contents/Info.plist"
	@plutil -replace CFBundleVersion -string "$(BUNDLE_VERSION)" "$(APP_BUNDLE)/Contents/Info.plist"
	@echo "APPL????" > "$(APP_BUNDLE)/Contents/PkgInfo"
	@echo "Signing app bundle..."
	@codesign --force --sign - --deep "$(APP_BUNDLE)"
	@echo "Created $(APP_BUNDLE)"

# Generate icons from the master image.
icons: $(ICON_ICNS)

# The master fills its canvas edge to edge. macOS lays app icons on a grid
# whose artwork occupies the centered 824 x 824 of a 1024 canvas with
# transparent margins, so the sizes are generated from a padded copy; an
# icon off the grid is scaled down and shown on a backing plate.
$(ICON_ICNS): $(ICON_MASTER) | $(BUILD_DIR)
	@echo "Generating macOS icon..."
	@mkdir -p $(ICONSET_DIR)
	@sips -s format png -z 824 824 $(ICON_MASTER) --out $(ICON_GRID)
	@sips -s format png -p 1024 1024 $(ICON_GRID) --out $(ICON_GRID)
	@sips -s format png -z 16 16 $(ICON_GRID) --out $(ICONSET_DIR)/icon_16x16.png
	@sips -s format png -z 32 32 $(ICON_GRID) --out $(ICONSET_DIR)/icon_16x16@2x.png
	@sips -s format png -z 32 32 $(ICON_GRID) --out $(ICONSET_DIR)/icon_32x32.png
	@sips -s format png -z 64 64 $(ICON_GRID) --out $(ICONSET_DIR)/icon_32x32@2x.png
	@sips -s format png -z 128 128 $(ICON_GRID) --out $(ICONSET_DIR)/icon_128x128.png
	@sips -s format png -z 256 256 $(ICON_GRID) --out $(ICONSET_DIR)/icon_128x128@2x.png
	@sips -s format png -z 256 256 $(ICON_GRID) --out $(ICONSET_DIR)/icon_256x256.png
	@sips -s format png -z 512 512 $(ICON_GRID) --out $(ICONSET_DIR)/icon_256x256@2x.png
	@sips -s format png -z 512 512 $(ICON_GRID) --out $(ICONSET_DIR)/icon_512x512.png
	@sips -s format png -z 1024 1024 $(ICON_GRID) --out $(ICONSET_DIR)/icon_512x512@2x.png
	@iconutil -c icns $(ICONSET_DIR) -o $(ICON_ICNS)
	@rm -rf $(ICONSET_DIR) $(ICON_GRID)
	@echo "Created $(ICON_ICNS)"

$(BUILD_DIR):
	@mkdir -p $(BUILD_DIR)

clean:
	rm -rf $(BUILD_DIR)
