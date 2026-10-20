# synk: the agent and command line in Go, the window in Tauri (www/).

.PHONY: build build-windows test vet ui-deps ui-dev ui-build ui-build-windows ui-install install

build:
	go build -o synk ./cmd/synk

build-windows:
	GOOS=windows GOARCH=amd64 go build -o synk.exe ./cmd/synk
	GOOS=windows GOARCH=amd64 go build -ldflags="-H=windowsgui" -o synk-agent.exe ./cmd/synk-agent

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

# Linux: a .deb in www/src-tauri/target/release/bundle/deb.
ui-build: ui-deps
	cd www && npx tauri build --bundles deb

# Instala o .deb da janela (aparece no menu de apps). hacks/install.sh instala só
# a CLI e o agente; a janela é um pacote à parte.
ui-install: ui-build
	sudo apt install ./www/src-tauri/target/release/bundle/deb/synk_0.1.0_amd64.deb

# Windows: built on Windows itself, because cross-compiling Tauri to Windows
# from Linux is fragile. Builds synk.exe and synk-agent.exe straight into the
# resources the NSIS installer embeds, so the installer that comes out
# carries the agent and installs it (see www/src-tauri/windows/installer.nsh)
# instead of just the window.
ui-build-windows: ui-deps
	GOOS=windows GOARCH=amd64 go build -o www/src-tauri/resources/synk.exe ./cmd/synk
	GOOS=windows GOARCH=amd64 go build -ldflags="-H=windowsgui" -o www/src-tauri/resources/synk-agent.exe ./cmd/synk-agent
	cd www && npx tauri build --bundles nsis

install:
	./hacks/install.sh
