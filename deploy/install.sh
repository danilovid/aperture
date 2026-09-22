#!/usr/bin/env bash
# Put a freshly built Aperture in place and start it.
#
# This runs on the server, from ~/incoming, where the pipeline has just left
# the binary, the console bundle and the Caddy site file. It lives in the
# repository rather than in the workflow so that what happens on the machine
# is reviewable, and so that a person can run exactly the same steps by hand
# when the pipeline is not the thing they want to debug.
#
# It is written to be safe to re-run and to put things back if the new build
# does not come up.

set -euo pipefail

APERTURE_DOMAIN="${APERTURE_DOMAIN:?the domain this installation answers on}"
VERSION="${VERSION:-unknown}"

BIN=/usr/local/bin/aperture
PREV=/usr/local/bin/aperture.prev
CONSOLE=/var/www/aperture-console
CONSOLE_PREV=/var/www/aperture-console.prev
SITE=/etc/caddy/aperture.caddy

cd "$(dirname "$0")"

say() { printf '\n▸ %s\n' "$*"; }

say "deploying $VERSION to $APERTURE_DOMAIN"

# ── the gateway ──────────────────────────────────────────────────────────────
# A running executable cannot be written to, but it can be renamed over: the
# old inode stays alive until the process restarts. So the new binary is put
# beside the old one first and then moved into place in one step, and there is
# never a moment where the path holds half a file.
say "installing the binary"
chmod +x aperture
sudo cp -f "$BIN" "$PREV" 2>/dev/null || true
sudo install -m 0755 -o root -g root aperture "$BIN.new"
sudo mv -f "$BIN.new" "$BIN"

# ── the console ──────────────────────────────────────────────────────────────
# Unpacked beside the live directory and swapped in, for the same reason: a
# half-extracted bundle is a broken site, and extracting over the live one
# would also leave files from the previous build behind for ever.
say "installing the console"
rm -rf console && mkdir console
tar -xzf console.tar.gz -C console
sudo rm -rf "$CONSOLE.new"
sudo cp -r console "$CONSOLE.new"
sudo chown -R root:root "$CONSOLE.new"
sudo find "$CONSOLE.new" -type d -exec chmod 755 {} +
sudo find "$CONSOLE.new" -type f -exec chmod 644 {} +
sudo rm -rf "$CONSOLE_PREV"
if [ -d "$CONSOLE" ]; then sudo mv "$CONSOLE" "$CONSOLE_PREV"; fi
sudo mv "$CONSOLE.new" "$CONSOLE"

# ── the routes ───────────────────────────────────────────────────────────────
# The site file is versioned with the code because the routes are part of the
# code: a release that adds an endpoint and a proxy that does not know about
# it is a release that half works.
say "installing the Caddy site"
sed "s|__APERTURE_DOMAIN__|$APERTURE_DOMAIN|g" aperture.caddy > site.caddy
if ! sudo cmp -s site.caddy "$SITE"; then
	sudo cp -f "$SITE" "$SITE.prev" 2>/dev/null || true
	sudo install -m 0644 -o root -g root site.caddy "$SITE"
	if sudo caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile; then
		sudo systemctl reload caddy
		echo "  routes updated"
	else
		echo "  the new Caddy config does not validate; keeping the old one" >&2
		sudo mv -f "$SITE.prev" "$SITE"
		exit 1
	fi
else
	echo "  unchanged"
fi

# ── restart, and put it back if it does not come up ──────────────────────────
say "restarting the service"
sudo systemctl restart aperture

healthy() {
	for _ in $(seq 1 15); do
		if curl -fsS --max-time 2 http://127.0.0.1:8088/health > /dev/null; then
			return 0
		fi
		sleep 1
	done
	return 1
}

if healthy; then
	say "$VERSION is up"
	# Only now is the previous build no longer needed as a way back.
	sudo rm -rf "$CONSOLE_PREV"
	rm -rf ~/incoming/console ~/incoming/console.tar.gz
	exit 0
fi

echo "the new build did not answer /health — rolling back" >&2
sudo journalctl -u aperture -n 40 --no-pager >&2 || true

if [ -f "$PREV" ]; then
	sudo mv -f "$PREV" "$BIN"
fi
if [ -d "$CONSOLE_PREV" ]; then
	sudo rm -rf "$CONSOLE"
	sudo mv "$CONSOLE_PREV" "$CONSOLE"
fi
sudo systemctl restart aperture

if healthy; then
	echo "rolled back to the previous build, which is up" >&2
else
	echo "rolled back, and the previous build is not answering either" >&2
fi
exit 1
