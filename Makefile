.PHONY: all clean desktop macos icons

# Output directories
BUILD_DIR := build
ICONSET_DIR := $(BUILD_DIR)/icon.iconset
APP_NAME := erings
APP_BUNDLE := $(BUILD_DIR)/$(APP_NAME).app

# Source files
ICON_MASTER := assets/icon.png
ICON_ICNS := $(BUILD_DIR)/icon.icns

# Build all targets
all: desktop

# Build the desktop binary
desktop:
	go build -o $(BUILD_DIR)/erings ./cmd/desktop/

# Build macOS .app bundle
macos: desktop icons
	@echo "Creating $(APP_NAME).app bundle..."
	@mkdir -p "$(APP_BUNDLE)/Contents/MacOS"
	@mkdir -p "$(APP_BUNDLE)/Contents/Resources"
	@cp $(BUILD_DIR)/erings "$(APP_BUNDLE)/Contents/MacOS/"
	@cp $(ICON_ICNS) "$(APP_BUNDLE)/Contents/Resources/icon.icns"
	@cp assets/macos_info.plist "$(APP_BUNDLE)/Contents/Info.plist"
	@echo "APPL????" > "$(APP_BUNDLE)/Contents/PkgInfo"
	@echo "Signing app bundle..."
	@codesign --force --sign - --deep "$(APP_BUNDLE)"
	@echo "Created $(APP_BUNDLE)"

# Generate icons from master PNG
icons: $(ICON_ICNS)

$(ICON_ICNS): $(ICON_MASTER) | $(BUILD_DIR)
	@echo "Generating macOS icon..."
	@mkdir -p $(ICONSET_DIR)
	@sips -z 16 16 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_16x16.png
	@sips -z 32 32 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_16x16@2x.png
	@sips -z 32 32 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_32x32.png
	@sips -z 64 64 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_32x32@2x.png
	@sips -z 128 128 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_128x128.png
	@sips -z 256 256 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_128x128@2x.png
	@sips -z 256 256 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_256x256.png
	@sips -z 512 512 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_256x256@2x.png
	@sips -z 512 512 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_512x512.png
	@sips -z 1024 1024 $(ICON_MASTER) --out $(ICONSET_DIR)/icon_512x512@2x.png
	@iconutil -c icns $(ICONSET_DIR) -o $(ICON_ICNS)
	@rm -rf $(ICONSET_DIR)
	@echo "Created $(ICON_ICNS)"

$(BUILD_DIR):
	@mkdir -p $(BUILD_DIR)

clean:
	rm -rf $(BUILD_DIR)
