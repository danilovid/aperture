#!/usr/bin/env bash
# One-time setup on the server so the pipeline can deploy to it.
#
# Run as root, once, by a person. It creates the account the pipeline signs in
# as, the narrow sudo rights that account needs, and the directories the
# deploy writes to. Everything here is idempotent, so running it again after
# a server rebuild is safe.
#
# To undo it:
#   rm /etc/sudoers.d/aperture-deploy && userdel -r aperture-deploy
#
# What this account can do, plainly: replace the gateway binary, replace the
# console, rewrite Aperture's Caddy site file and restart those two services.
# That is enough to run anything as root on this machine, so the SSH key the
# pipeline holds is a production credential. Rotate it if the repository is
# ever compromised, the same way you would rotate a database password.

set -euo pipefail

USER_NAME=aperture-deploy
AUTHORIZED_KEY="${1:-}"

if [ "$(id -u)" != 0 ]; then
	echo "run this as root" >&2
	exit 1
fi
if [ -z "$AUTHORIZED_KEY" ]; then
	echo "usage: $0 '<ssh public key for the pipeline>'" >&2
	exit 1
fi

echo "▸ the account"
if ! id "$USER_NAME" > /dev/null 2>&1; then
	useradd --create-home --shell /bin/bash "$USER_NAME"
fi
install -d -m 0700 -o "$USER_NAME" -g "$USER_NAME" "/home/$USER_NAME/.ssh"
printf '%s\n' "$AUTHORIZED_KEY" > "/home/$USER_NAME/.ssh/authorized_keys"
chown "$USER_NAME:$USER_NAME" "/home/$USER_NAME/.ssh/authorized_keys"
chmod 600 "/home/$USER_NAME/.ssh/authorized_keys"

echo "▸ sudo rights"
# Listed one by one rather than granted wholesale: not because the list is a
# meaningful boundary — it is not, see the note above — but because it says
# what the deploy is supposed to touch, and anything else needs somebody to
# come here and add it on purpose.
cat > /etc/sudoers.d/aperture-deploy <<'SUDOERS'
# The account the Aperture pipeline deploys as.
aperture-deploy ALL=(root) NOPASSWD: \
	/usr/bin/install, \
	/usr/bin/cp, \
	/usr/bin/mv, \
	/usr/bin/rm, \
	/usr/bin/chown, \
	/usr/bin/chmod, \
	/usr/bin/find, \
	/usr/bin/cmp, \
	/usr/bin/systemctl restart aperture, \
	/usr/bin/systemctl reload caddy, \
	/usr/bin/systemctl status aperture, \
	/usr/bin/caddy validate *, \
	/usr/bin/journalctl -u aperture *
SUDOERS
chmod 0440 /etc/sudoers.d/aperture-deploy
visudo -c -f /etc/sudoers.d/aperture-deploy

echo "▸ directories"
install -d -m 0755 -o root -g root /var/www

echo
echo "done. The pipeline can now sign in as $USER_NAME."
echo "Its host key, for the DEPLOY_KNOWN_HOSTS repository variable:"
ssh-keyscan -t ed25519 "$(hostname -I | awk '{print $1}')" 2>/dev/null
