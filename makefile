# synk: the agent and command line in Go, the window in Tauri (www/).

.PHONY: build build-windows test vet ui-deps ui-dev ui-build install

build:
	go build -o synk ./cmd/synk

build-windows:
	GOOS=windows GOARCH=amd64 go build -o synk.exe ./cmd/synk

# The screen tests read the layout from the graphical session.
test:
	WAYLAND_DISPLAY=$${WAYLAND_DISPLAY:-wayland-0} go test -race ./...

vet:
	go vet ./...
	GOOS=windows go vet ./...

ui-deps:
	cd www && npm install

ui-dev: ui-deps
	cd www && npx tauri dev

# Linux: a .deb in www/src-tauri/target/release/bundle/deb. The Windows
# installer is built on Windows, with the same command and --bundles nsis:
# cross-compiling Tauri to Windows from Linux is fragile.
ui-build: ui-deps
	cd www && npx tauri build --bundles deb

install:
	./hacks/install.sh
