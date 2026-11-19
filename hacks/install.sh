#!/usr/bin/env bash
# Install synk on this Linux machine, migrating wfkit-link or conn if present.
#
# Run as your normal user; it asks for sudo only for the steps that genuinely
# need root, and prints each one before running it. Running it again is safe:
# every step checks before it acts.
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UNITS="$REPO/infra/linux"
BIN="$REPO/synk"
GROUP="synk"
# The virtual device's name, which KDE keys the pointer profile on. It must
# match inputdev.VirtualNamePrefix.
DEVICE="synk"

CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/synk"
CONFIG="$CONFIG_DIR/config.yaml"
USER_UNITS="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"

# Os nomes antigos, do mais novo para o mais velho. O protocolo de rede não
# mudou com eles, então um PC Windows ainda rodando o exe antigo continua
# conectando neste lado já migrado.
OLD_USER_UNITS=(conn.service wfkit-link.service)
OLD_SYSTEM_UNITS=(connd.service wfkit-linkd.service)
OLD_BINS=(/usr/local/bin/conn /usr/local/bin/wfkit-link)
OLD_GROUPS=(conn wfkit-link)
OLD_CONFIGS=("${XDG_CONFIG_HOME:-$HOME/.config}/conn/conn.yaml" "${XDG_CONFIG_HOME:-$HOME/.config}/wfkit/link.yaml")
OLD_DEVICES=(conn wfkit-link)

say() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }

say "Building"
(cd "$REPO" && GOTOOLCHAIN=auto go build -o synk ./cmd/synk)
test -x "$BIN"

# --- backup ------------------------------------------------------------------
# Tudo o que esta migração apaga ou reescreve ganha uma cópia antes, num lugar
# só, para dar para voltar atrás sem depender da memória de ninguém.
BACKUP="${XDG_DATA_HOME:-$HOME/.local/share}/synk/backup-$(date +%Y%m%d-%H%M%S)"
backup() {
	local f="$1"
	[ -e "$f" ] || return 0
	mkdir -p "$BACKUP"
	# ~/.config/wfkit/link.yaml -> home_.config_wfkit_link.yaml, and
	# /etc/systemd/system/x.service -> etc_systemd_system_x.service
	local name="${f/#"$HOME"\//home/}"
	cp -p "$f" "$BACKUP/$(echo "${name#/}" | tr / _)"
}
for f in "${OLD_CONFIGS[@]}" "${XDG_CONFIG_HOME:-$HOME/.config}/kcminputrc"; do backup "$f"; done
for u in "${OLD_USER_UNITS[@]}"; do backup "$USER_UNITS/$u"; done
for u in "${OLD_SYSTEM_UNITS[@]}"; do backup "/etc/systemd/system/$u"; done
[ -d "$BACKUP" ] && say "Backed up the old install to $BACKUP"

# --- config ------------------------------------------------------------------
if [ ! -e "$CONFIG" ]; then
	for old in "${OLD_CONFIGS[@]}"; do
		[ -f "$old" ] || continue
		say "Copying the config from $old to $CONFIG"
		mkdir -p "$CONFIG_DIR"
		cp -p "$old" "$CONFIG"
		# Um socket com o caminho antigo apontaria para um diretório que deixa
		# de existir; sem a linha, vale o padrão, que casa com a unit nova.
		sed -i -E '/^socket:.*(wfkit-link|\/conn\/)/d' "$CONFIG"
		break
	done
fi
if [ ! -e "$CONFIG" ]; then
	say "No config yet"
	echo "Write $CONFIG naming the peer and the edge its screen sits beyond;"
	echo "'synk doctor' at the end says exactly what is missing."
fi

# --- group -------------------------------------------------------------------
# The group gates the injector's socket. Being in it means being able to ask
# for input to be injected — not to read input, which the injector never does.
#
# An old group is RENAMED rather than replaced: the GID stays the same, and the
# GID is what running processes carry. So a machine that had wfkit-link keeps
# working without logging out, which a brand-new group would force.
NEED_RELOGIN=0
if ! getent group "$GROUP" >/dev/null; then
	renamed=0
	for old in "${OLD_GROUPS[@]}"; do
		if getent group "$old" >/dev/null; then
			say "Renaming the $old group to $GROUP (same GID, so no logout needed)"
			sudo groupmod -n "$GROUP" "$old"
			renamed=1
			break
		fi
	done
	if [ "$renamed" = 0 ]; then
		say "Creating the $GROUP group"
		sudo groupadd "$GROUP"
	fi
fi
if ! id -nG "$USER" | tr ' ' '\n' | grep -qx "$GROUP"; then
	say "Adding $USER to the $GROUP group"
	sudo usermod -aG "$GROUP" "$USER"
	NEED_RELOGIN=1
fi

# --- binary and units ----------------------------------------------------------
say "Installing the binary to /usr/local/bin/synk"
sudo install -m 0755 "$BIN" /usr/local/bin/synk

say "Installing the systemd units"
sudo install -m 0644 "$UNITS/synk-injector.service" /etc/systemd/system/
mkdir -p "$USER_UNITS"
install -m 0644 "$UNITS/synk-agent.service" "$USER_UNITS/"

# Nothing starts at boot. The CLI and the window start synk when used, and the
# injector is a system unit, so this rule lets the synk group start it without
# a password — and only it, only start/stop/restart.
say "Installing the polkit rule that lets 'synk up' start the injector"
sudo install -m 0644 "$UNITS/50-synk-injector.rules" /etc/polkit-1/rules.d/

say "Setting a flat pointer profile for the virtual device"
# The agent places the cursor by injecting a relative delta, and that only
# lands where intended when one injected unit moves one pixel. Under libinput's
# default adaptive curve it does not: measured on this machine, an intended 500
# pixels travelled 934, and a cursor crossing over from the PC arrived nowhere
# near the height it left at.
#
# The device's vendor and product are fixed in encodeSetup(): 0x1d6b/0x0001,
# which KDE writes in decimal. The entry is keyed on the device's name, so the
# old names' entries go: they describe devices that no longer exist.
kwriteconfig6 --file kcminputrc --group Libinput --group 7531 --group 1 \
	--group "$DEVICE" --key PointerAccelerationProfile 1
kwriteconfig6 --file kcminputrc --group Libinput --group 7531 --group 1 \
	--group "$DEVICE" --key PointerAcceleration 0
for old in "${OLD_DEVICES[@]}"; do
	for key in PointerAccelerationProfile PointerAcceleration; do
		kwriteconfig6 --file kcminputrc --group Libinput --group 7531 --group 1 \
			--group "$old" --key "$key" --delete
	done
done

# --- the switch ----------------------------------------------------------------
# Old off, new on, back to back: the link is down only between these lines.
# Both would bind the same port and each would create its own virtual device,
# so they cannot overlap.
for u in "${OLD_USER_UNITS[@]}"; do
	if [ -e "$USER_UNITS/$u" ]; then
		say "Removing $u"
		systemctl --user disable --now "$u" 2>/dev/null || true
		rm -f "$USER_UNITS/$u"
	fi
done
for u in "${OLD_SYSTEM_UNITS[@]}"; do
	if [ -e "/etc/systemd/system/$u" ]; then
		say "Removing $u"
		sudo systemctl disable --now "$u" 2>/dev/null || true
		sudo rm -f "/etc/systemd/system/$u"
	fi
done
for b in "${OLD_BINS[@]}"; do
	if [ -e "$b" ]; then
		say "Removing $b"
		sudo rm -f "$b"
	fi
done

# On demand, not at boot: `disable` drops the symlink an earlier install made,
# so a machine upgrading from "always on" stops starting synk at login. The
# restart below is only so the checks that follow have something to check.
say "Starting the injector (not enabled: it starts when synk is used)"
sudo systemctl daemon-reload
sudo systemctl disable synk-injector.service 2>/dev/null || true
sudo systemctl restart synk-injector.service

say "Starting the agent (not enabled: it starts when synk is used)"
systemctl --user daemon-reload
systemctl --user disable synk-agent.service 2>/dev/null || true
systemctl --user restart synk-agent.service

say "Checking"
# The agent needs a moment to find Tailscale and bind the port.
sleep 2
/usr/local/bin/synk status || true
echo
/usr/local/bin/synk doctor || true

if [ "$NEED_RELOGIN" = 1 ]; then
	cat <<EOF

  * LOG OUT AND LOG BACK IN before using the link. There is no shortcut.

    You were just added to the $GROUP group, and group membership only reaches
    processes started after it was granted — including \`systemd --user\`, which
    was launched at login. Until then the agent cannot open the injector's
    socket, and 'synk doctor' will say exactly that.

    Do NOT reach for \`su -\` instead. It does give you the new group, but a
    login shell drops XDG_RUNTIME_DIR and WAYLAND_DISPLAY, and without those
    kscreen-doctor aborts and dumps core.
EOF
fi

cat <<'EOF'

  * Verify the injector really has no network, which is the point of the
    unit's hardening:
        sudo nsenter -t "$(systemctl show -p MainPID --value synk-injector)" -n ip addr
    It should show only a loopback interface.
EOF
