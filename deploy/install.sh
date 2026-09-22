#!/usr/bin/env bash
# Put a freshly built Aperture in place and start it.
#
# This runs on the server, from ~/incoming, where the pipeline has just left
# the binary, the console bundle and the Caddy site file. It lives in the
# repository rather than in the workflow so that what happens on the machine
# is reviewable, and so that a person can run exactly the same steps by hand
# when the pipeline is not the thing they want to debug.
#
# The shape of it: stage everything beside the live files first, touching
# nothing; then swap things in one at a time, remembering each swap; and if
# anything at all fails after the first swap, put every swapped thing back.
# That last part is a trap on exit rather than a line after each step,
# because a line after each step is exactly what gets forgotten on the one
# step that fails.
#
# What it cannot put back is the database. The gateway migrates its schema in
# place when it starts, forward only, so a rollback restores the previous
# binary onto the new schema. Take a backup before a release that changes the
# schema; docs/DEPLOY.md says how.

set -euo pipefail

APERTURE_DOMAIN="${APERTURE_DOMAIN:?the domain this installation answers on}"
VERSION="${VERSION:-unknown}"

# Overridable only so deploy/install_test.sh can run this against a scratch
# directory; on the server the defaults are the truth.
BIN="${APERTURE_BIN:-/usr/local/bin/aperture}"
CONSOLE="${APERTURE_CONSOLE:-/var/www/aperture-console}"
SITE="${APERTURE_SITE:-/etc/caddy/aperture.caddy}"
HEALTH_URL="${APERTURE_HEALTH_URL:-http://127.0.0.1:8088/health}"
HEALTH_TRIES="${APERTURE_HEALTH_TRIES:-15}"

cd "$(dirname "$0")"

say() { printf '\n▸ %s\n' "$*"; }

# What has been swapped so far, so the rollback undoes exactly that.
swapped_site=0
swapped_bin=0
swapped_console=0
# On a first install there is no previous build: undoing then means removing
# the new one rather than restoring an old one.
had_site=0
had_bin=0
had_console=0
restarted=0
committed=0

rollback() {
	[ "$committed" = 1 ] && return
	# Carry on through the rollback even if one step of it fails: stopping
	# halfway through putting things back is the worst of both worlds.
	set +e
	echo >&2
	echo "▸ something failed — putting back what was changed" >&2
	if [ "$swapped_site" = 1 ]; then
		if [ "$had_site" = 1 ]; then sudo mv -f "$SITE.prev" "$SITE"; else sudo rm -f "$SITE"; fi
		sudo systemctl reload caddy || echo "  caddy did not reload the previous site file" >&2
		echo "  routes restored" >&2
	fi
	if [ "$swapped_bin" = 1 ]; then
		if [ "$had_bin" = 1 ]; then sudo mv -f "$BIN.prev" "$BIN"; else sudo rm -f "$BIN"; fi
		echo "  binary restored" >&2
	fi
	if [ "$swapped_console" = 1 ]; then
		sudo rm -rf "$CONSOLE"
		if [ "$had_console" = 1 ]; then sudo mv "$CONSOLE.prev" "$CONSOLE"; fi
		echo "  console restored" >&2
	fi
	# Whatever was staged and never swapped in goes too.
	sudo rm -rf "$BIN.new" "$CONSOLE.new"
	# Only restart if this run restarted it: otherwise the old process is still
	# the one serving, and a restart would be an outage for nothing.
	if [ "$restarted" = 1 ]; then
		sudo systemctl restart aperture
		if healthy; then
			echo "  the previous build is up again" >&2
		else
			echo "  the previous build is not answering either — look at the journal" >&2
		fi
	fi
}
trap rollback EXIT

healthy() {
	for _ in $(seq 1 "$HEALTH_TRIES"); do
		# Quiet on purpose: the first probe usually lands before the process is
		# listening, and a "connection refused" in a successful deploy's log
		# is a false alarm somebody will chase.
		if curl -fs --max-time 2 "$HEALTH_URL" > /dev/null 2>&1; then
			return 0
		fi
		sleep 1
	done
	return 1
}

say "deploying $VERSION to $APERTURE_DOMAIN"

# ── stage: nothing live is touched yet ───────────────────────────────────────
say "staging"
chmod +x aperture
sudo install -m 0755 -o root -g root aperture "$BIN.new"

rm -rf console && mkdir console
# The archive may carry extended headers from whatever built it; they mean
# nothing here and only make the log look like something went wrong.
tar --warning=no-unknown-keyword -xzf console.tar.gz -C console
sudo rm -rf "$CONSOLE.new"
sudo cp -r console "$CONSOLE.new"
sudo chown -R root:root "$CONSOLE.new"
sudo find "$CONSOLE.new" -type d -exec chmod 755 {} +
sudo find "$CONSOLE.new" -type f -exec chmod 644 {} +

sed "s|__APERTURE_DOMAIN__|$APERTURE_DOMAIN|g" aperture.caddy > site.caddy
echo "  staged"

# ── the routes first ─────────────────────────────────────────────────────────
# New routes in front of the old binary are harmless — a path it does not know
# answers 404, as it would have anyway. The reverse is not: a new binary behind
# old routes has endpoints nobody can reach. So routes go first.
#
# Caddy is reloaded through systemd rather than validated from here, because
# the main Caddyfile reads its domains from the unit's environment, which a
# shell does not have. The reload is the validation, and it is transactional:
# a config Caddy cannot load leaves the running one in place.
say "routes"
if sudo cmp -s site.caddy "$SITE"; then
	echo "  unchanged"
else
	if [ -e "$SITE" ]; then
		sudo cp -f "$SITE" "$SITE.prev"
		had_site=1
	fi
	sudo install -m 0644 -o root -g root site.caddy "$SITE"
	swapped_site=1
	sudo systemctl reload caddy
	echo "  updated"
fi

# ── the gateway and the console ──────────────────────────────────────────────
# A running executable cannot be written to, but it can be renamed over: the
# process keeps the old inode until it restarts. The console is swapped as a
# directory, so there is never a half-extracted site, and no file from the
# previous build is left behind.
say "swapping the build in"
if [ -e "$BIN" ]; then
	sudo cp -f "$BIN" "$BIN.prev"
	had_bin=1
fi
sudo mv -f "$BIN.new" "$BIN"
swapped_bin=1

sudo rm -rf "$CONSOLE.prev"
if [ -d "$CONSOLE" ]; then
	sudo mv "$CONSOLE" "$CONSOLE.prev"
	had_console=1
fi
sudo mv "$CONSOLE.new" "$CONSOLE"
swapped_console=1

say "restarting"
restarted=1
sudo systemctl restart aperture
if ! healthy; then
	echo "the new build did not answer /health within fifteen seconds" >&2
	sudo journalctl -u aperture -n 40 --no-pager >&2 || true
	exit 1
fi

committed=1
say "$VERSION is up"
# The previous build is kept as the way back until the next deploy replaces
# it: the binary as aperture.prev, the console as aperture-console.prev.
rm -rf console console.tar.gz site.caddy
