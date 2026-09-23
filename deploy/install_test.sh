#!/usr/bin/env bash
# Tests for deploy/install.sh: every way a deploy can end, against a scratch
# directory instead of a server.
#
# install.sh is the one piece of this project that runs as root on a machine
# people depend on, and its whole value is in the paths that are rarely taken
# — the rollbacks. Those are exactly the paths nobody exercises by hand, so
# they are exercised here. systemctl, sudo and curl are stubbed; the files,
# tar, install and the trap logic are real.
#
# It wants root (install -o root) and GNU tar, so it runs in a container:
#
#   docker run --rm -v "$PWD:/src" -w /src ubuntu:24.04 bash deploy/install_test.sh

set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
failures=0
pass() { printf '  ✓ %s\n' "$*"; }
fail() { printf '  ✗ %s\n' "$*"; failures=$((failures + 1)); }
expect() { if eval "$1"; then pass "$2"; else fail "$2"; fi; }

# A fresh root per scenario, with stubbed system commands that record what
# they were asked to do.
setup() {
	ROOT="$(mktemp -d)"
	mkdir -p "$ROOT/bin" "$ROOT/stubs" "$ROOT/www" "$ROOT/caddy" "$ROOT/incoming"
	export APERTURE_BIN="$ROOT/bin/aperture"
	export APERTURE_CONSOLE="$ROOT/www/aperture-console"
	export APERTURE_SITE="$ROOT/caddy/aperture.caddy"
	export APERTURE_HEALTH_URL="http://stub/health"
	export APERTURE_HEALTH_TRIES=2
	export APERTURE_DOMAIN="aperture.example.test"
	export VERSION="test"
	export CALLS="$ROOT/calls" RUNNING="$ROOT/running"
	: > "$CALLS"

	# We are root already, so sudo only has to get out of the way.
	cat > "$ROOT/stubs/sudo" <<'STUB'
#!/usr/bin/env bash
exec "$@"
STUB
	# systemctl remembers what it was asked. Restarting "aperture" means the
	# binary currently at APERTURE_BIN becomes the running one.
	cat > "$ROOT/stubs/systemctl" <<'STUB'
#!/usr/bin/env bash
echo "systemctl $*" >> "$CALLS"
case "$1 $2" in
	"reload caddy") [ "${FAIL_RELOAD:-0}" = 1 ] && exit 1 ;;
	"restart aperture") cp "$APERTURE_BIN" "$RUNNING" ;;
esac
exit 0
STUB
	# /health answers unless the running build is a broken one.
	cat > "$ROOT/stubs/curl" <<'STUB'
#!/usr/bin/env bash
[ -f "$RUNNING" ] && ! grep -q BROKEN "$RUNNING"
STUB
	printf '#!/bin/sh\necho "(journal)"\n' > "$ROOT/stubs/journalctl"
	# Health retries sleep between attempts; the tests have nowhere to be.
	printf '#!/bin/sh\nexit 0\n' > "$ROOT/stubs/sleep"
	chmod +x "$ROOT/stubs/"*
	export PATH="$ROOT/stubs:$PATH"

	# What the pipeline leaves in ~/incoming.
	cp "$HERE/install.sh" "$HERE/aperture.caddy" "$ROOT/incoming/"
	mkdir -p "$ROOT/console-src/assets"
	echo "new console" > "$ROOT/console-src/index.html"
	echo "new asset" > "$ROOT/console-src/assets/app.js"
	tar -czf "$ROOT/incoming/console.tar.gz" -C "$ROOT/console-src" .
}

# A previous build already in place, as on a server that has been deployed to.
previous_build() {
	echo "old binary" > "$APERTURE_BIN"
	mkdir -p "$APERTURE_CONSOLE"
	echo "old console" > "$APERTURE_CONSOLE/index.html"
	echo "stale file from the old build" > "$APERTURE_CONSOLE/stale.js"
	echo "old routes" > "$APERTURE_SITE"
	cp "$APERTURE_BIN" "$RUNNING"
}

new_binary() { echo "$1" > "$ROOT/incoming/mutegate"; }

run() {
	bash "$ROOT/incoming/install.sh" > "$ROOT/out" 2>&1
	echo $?
}

teardown() { rm -rf "$ROOT"; unset FAIL_RELOAD; }

echo "▸ an upgrade that goes well"
setup; previous_build; new_binary "new binary"
code=$(run)
expect '[ "$code" = 0 ]' "exits 0"
expect 'grep -q "new binary" "$APERTURE_BIN"' "the new binary is in place"
expect 'grep -q "old binary" "$APERTURE_BIN.prev"' "the previous binary is kept as the way back"
expect 'grep -q "new console" "$APERTURE_CONSOLE/index.html"' "the new console is in place"
expect '[ ! -e "$APERTURE_CONSOLE/stale.js" ]' "no file from the old console is left behind"
expect 'grep -q "old console" "$APERTURE_CONSOLE.prev/index.html"' "the previous console is kept"
expect 'grep -q "aperture.example.test {" "$APERTURE_SITE"' "the site file has the domain filled in"
expect 'grep -q "reload caddy" "$CALLS"' "caddy was reloaded for the new routes"
expect 'grep -q "restart aperture" "$CALLS"' "the service was restarted"
expect 'grep -q "new binary" "$RUNNING"' "the new build is the one running"
expect '[ ! -e "$APERTURE_BIN.new" ] && [ ! -e "$APERTURE_CONSOLE.new" ]' "nothing staged is left lying around"
teardown

echo "▸ routes that have not changed"
setup; previous_build; new_binary "new binary"
sed "s|__APERTURE_DOMAIN__|$APERTURE_DOMAIN|g" "$HERE/aperture.caddy" > "$APERTURE_SITE"
code=$(run)
expect '[ "$code" = 0 ]' "exits 0"
expect '! grep -q "reload caddy" "$CALLS"' "caddy is left alone"
teardown

echo "▸ a Caddy config that does not load"
setup; previous_build; new_binary "new binary"
export FAIL_RELOAD=1
code=$(run)
expect '[ "$code" != 0 ]' "fails"
expect 'grep -q "old routes" "$APERTURE_SITE"' "the previous site file is back"
expect 'grep -q "old binary" "$APERTURE_BIN"' "the binary was never swapped"
expect 'grep -q "old console" "$APERTURE_CONSOLE/index.html"' "the console was never swapped"
expect '! grep -q "restart aperture" "$CALLS"' "the service was not restarted — the old one kept serving"
expect 'grep -q "old binary" "$RUNNING"' "the old build is still the one running"
expect '[ ! -e "$APERTURE_BIN.new" ] && [ ! -e "$APERTURE_CONSOLE.new" ]' "what was staged is cleaned up"
teardown

echo "▸ a new build that does not come up"
setup; previous_build; new_binary "BROKEN binary"
code=$(run)
expect '[ "$code" != 0 ]' "fails"
expect 'grep -q "old binary" "$APERTURE_BIN"' "the previous binary is back"
expect 'grep -q "old console" "$APERTURE_CONSOLE/index.html"' "the previous console is back"
expect 'grep -q "stale.js" <(ls "$APERTURE_CONSOLE")' "including the files only it had"
expect 'grep -q "old routes" "$APERTURE_SITE"' "the previous routes are back"
expect '[ "$(grep -c "restart aperture" "$CALLS")" = 2 ]' "restarted twice: into the new build, then back"
expect 'grep -q "old binary" "$RUNNING"' "the old build is the one running again"
expect 'grep -q "the previous build is up again" "$ROOT/out"' "and says so"
teardown

echo "▸ a first install"
setup; new_binary "new binary"
code=$(run)
expect '[ "$code" = 0 ]' "exits 0 with nothing to replace"
expect 'grep -q "new binary" "$APERTURE_BIN"' "the binary is in place"
expect 'grep -q "new console" "$APERTURE_CONSOLE/index.html"' "the console is in place"
expect '[ -e "$APERTURE_SITE" ]' "the site file is in place"
teardown

echo "▸ a first install that does not come up"
setup; new_binary "BROKEN binary"
code=$(run)
expect '[ "$code" != 0 ]' "fails"
expect '[ ! -e "$APERTURE_BIN" ]' "the broken binary is removed, not left as the only build"
expect '[ ! -e "$APERTURE_CONSOLE" ]' "the console is removed"
expect '[ ! -e "$APERTURE_SITE" ]' "the site file is removed"
teardown

echo
if [ "$failures" = 0 ]; then
	echo "all good"
else
	echo "$failures failed"
	exit 1
fi
