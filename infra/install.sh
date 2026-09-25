#!/usr/bin/env bash
# Install synk on this Linux machine.
#
# Run as your normal user; it asks for sudo only for the steps that genuinely
# need root, and prints each one before running it.
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UNITS="$REPO/infra"
BIN="$REPO/synk"
GROUP="conn"

say() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }

say "Building"
(cd "$REPO" && GOTOOLCHAIN=auto go build -o synk ./cmd/synk)
test -x "$BIN"

# --- migração do nome antigo (wfkit-link, até 2026-09-24) ------------------
# Os dois serviços antigos seguram /dev/uinput e o socket do daemon: se
# ficassem ligados, o novo e o velho brigariam pelo mesmo dispositivo. Então
# saem antes de o novo entrar. A config vem junto, uma vez só.
if [ -f /etc/systemd/system/wfkit-linkd.service ] || [ -f ~/.config/systemd/user/wfkit-link.service ] \
   || [ -e /usr/local/bin/wfkit-link ]; then
	say "Removing the old wfkit-link install"
	systemctl --user disable --now wfkit-link.service 2>/dev/null || true
	rm -f ~/.config/systemd/user/wfkit-link.service
	sudo systemctl disable --now wfkit-linkd.service 2>/dev/null || true
	sudo rm -f /etc/systemd/system/wfkit-linkd.service /usr/local/bin/wfkit-link
	# o grupo antigo só servia ao socket antigo
	getent group wfkit-link >/dev/null && sudo groupdel wfkit-link
fi
if [ -f ~/.config/wfkit/link.yaml ] && [ ! -e ~/.config/conn/conn.yaml ]; then
	say "Moving the config from ~/.config/wfkit/link.yaml to ~/.config/conn/conn.yaml"
	mkdir -p ~/.config/conn
	cp -p ~/.config/wfkit/link.yaml ~/.config/conn/conn.yaml
fi

say "Installing the binary to /usr/local/bin"
sudo install -m 0755 "$BIN" /usr/local/bin/synk

say "Creating the $GROUP group and adding $USER to it"
# The group gates the daemon's socket. Being in it means being able to ask for
# input to be injected — not to read input, which the daemon never does.
sudo groupadd -f "$GROUP"
sudo usermod -aG "$GROUP" "$USER"

say "Installing the systemd units"
sudo install -m 0644 "$UNITS/connd.service" /etc/systemd/system/
mkdir -p ~/.config/systemd/user
install -m 0644 "$UNITS/conn.service" ~/.config/systemd/user/

say "Setting a flat pointer profile for the virtual device"
# The session places the cursor by injecting a relative delta, and that only
# lands where intended when one injected unit moves one pixel. Under libinput's
# default adaptive curve it does not: measured on this machine, an intended 500
# pixels travelled 934, and a cursor crossing over from the PC arrived nowhere
# near the height it left at.
#
# The device's vendor and product are fixed in encodeSetup(): 0x1d6b/0x0001,
# which KDE writes in decimal.
kwriteconfig6 --file kcminputrc --group Libinput --group 7531 --group 1 \
	--group "$GROUP" --key PointerAccelerationProfile 1
kwriteconfig6 --file kcminputrc --group Libinput --group 7531 --group 1 \
	--group "$GROUP" --key PointerAcceleration 0

say "Enabling the privileged daemon"
sudo systemctl daemon-reload
sudo systemctl enable --now connd.service

say "Enabling the user session"
systemctl --user daemon-reload
systemctl --user enable --now conn.service

say "Checking"
sudo systemctl --no-pager --lines=0 status connd.service || true
/usr/local/bin/synk doctor || true

cat <<'EOF'

Two things worth knowing:

  * LOG OUT AND LOG BACK IN before using the link. There is no shortcut.

    Group membership only reaches processes started after it was granted, and
    that includes `systemd --user`, which was launched at login and still
    carries the old set — so the session unit cannot open the daemon's socket
    until the whole session restarts.

    Do NOT reach for `su -` instead. It does give you the new group, but a
    login shell drops XDG_RUNTIME_DIR and WAYLAND_DISPLAY, and without those
    kscreen-doctor aborts and dumps core. You would trade one failure for a
    stranger one.

    Check whether the user manager has caught up with:
        grep ^Groups: /proc/$(pgrep -u "$USER" -x systemd | head -1)/status
    and compare against:  getent group conn

  * Verify the daemon really has no network, which is the point of the unit's
    hardening:
        sudo nsenter -t "$(pidof synk | cut -d' ' -f1)" -n ip addr
    It should show only a loopback interface.
EOF
