.PHONY: all clean desktop windows macos appimage icons

# Output directories
BUILD_DIR := build
ICONSET_DIR := $(BUILD_DIR)/icon.iconset
APP_NAME := erings
APP_BUNDLE := $(BUILD_DIR)/$(APP_NAME).app
APPDIR := $(BUILD_DIR)/AppDir

# AppImage architecture. Defaults to the host machine's architecture so a local
# build produces a matching AppImage. CI overrides this (make ... APPIMAGE_ARCH=)
# to label cross-runner artifacts. The value follows the AppImage/linuxdeploy
# naming convention (x86_64, aarch64).
APPIMAGE_ARCH ?= $(shell uname -m)

# Source files
ICON_MASTER := cmd/desktop/icon.webp
ICON_PNG := packaging/icon-512.png
ICON_ICNS := $(BUILD_DIR)/icon.icns
DESKTOP_FILE := packaging/erings.desktop

# AppImage tooling. linuxdeploy and its appimage plugin must be on PATH.
LINUXDEPLOY ?= linuxdeploy

# Version stamped into the binary with -ldflags. Supplied on the command line
# (make ... VERSION=...) so CI can stamp the same value it uses for artifact
# filenames. When empty (a plain local build or a source tarball) no version
# is injected and the binary resolves its version at runtime from the archive
# substitution or embedded build info.
VERSION ?=
ifneq ($(VERSION),)
VERSION_LDFLAGS := -X github.com/user-none/erings.Version=$(VERSION)
endif

# The version segment is dropped when no VERSION is supplied (plain local builds).
ifneq ($(VERSION),)
APPIMAGE_NAME := $(APP_NAME)-$(VERSION)-$(APPIMAGE_ARCH).AppImage
else
APPIMAGE_NAME := $(APP_NAME)-$(APPIMAGE_ARCH).AppImage
endif
APPIMAGE := $(BUILD_DIR)/$(APPIMAGE_NAME)

# Build all targets
all: desktop

# Build the desktop binary
desktop:
	go build -ldflags "$(VERSION_LDFLAGS)" -o $(BUILD_DIR)/erings ./cmd/desktop/

# Build the Windows desktop binary as a GUI subsystem app so no console
# window is opened when it runs.
windows:
	go build -ldflags '-H=windowsgui -s -w -extldflags "-static -lpthread" $(VERSION_LDFLAGS)' -o $(BUILD_DIR)/erings.exe ./cmd/desktop/

# Build a Linux AppImage.
appimage:
	go build -ldflags "-s -w $(VERSION_LDFLAGS)" -o $(BUILD_DIR)/erings ./cmd/desktop/
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
	go build -ldflags "-s -w $(VERSION_LDFLAGS)" -o $(BUILD_DIR)/erings ./cmd/desktop/
	@echo "Creating $(APP_NAME).app bundle..."
	@mkdir -p "$(APP_BUNDLE)/Contents/MacOS"
	@mkdir -p "$(APP_BUNDLE)/Contents/Resources"
	@cp $(BUILD_DIR)/erings "$(APP_BUNDLE)/Contents/MacOS/"
	@cp $(ICON_ICNS) "$(APP_BUNDLE)/Contents/Resources/icon.icns"
	@cp packaging/macos_info.plist "$(APP_BUNDLE)/Contents/Info.plist"
	@echo "APPL????" > "$(APP_BUNDLE)/Contents/PkgInfo"
	@echo "Signing app bundle..."
	@codesign --force --sign - --deep "$(APP_BUNDLE)"
	@echo "Created $(APP_BUNDLE)"

# Generate icons from the master image.
icons: $(ICON_ICNS)

$(ICON_ICNS): $(ICON_MASTER) | $(BUILD_DIR)
	@echo "Generating macOS icon..."
	@mkdir -p $(ICONSET_DIR)
	@sips -s format png -z 16 16 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_16x16.png
	@sips -s format png -z 32 32 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_16x16@2x.png
	@sips -s format png -z 32 32 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_32x32.png
	@sips -s format png -z 64 64 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_32x32@2x.png
	@sips -s format png -z 128 128 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_128x128.png
	@sips -s format png -z 256 256 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_128x128@2x.png
	@sips -s format png -z 256 256 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_256x256.png
	@sips -s format png -z 512 512 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_256x256@2x.png
	@sips -s format png -z 512 512 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_512x512.png
	@sips -s format png -z 1024 1024 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_512x512@2x.png
	@iconutil -c icns $(ICONSET_DIR) -o $(ICON_ICNS)
	@rm -rf $(ICONSET_DIR)
	@echo "Created $(ICON_ICNS)"

$(BUILD_DIR):
	@mkdir -p $(BUILD_DIR)

clean:
	rm -rf $(BUILD_DIR)
