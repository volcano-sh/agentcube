#!/bin/sh
set -e

# Setup SSH public key if provided via environment variable
if [ -n "$SSH_PUBLIC_KEY" ]; then
    echo "Setting up SSH public key..."
    mkdir -p /home/sandbox/.ssh
    chmod 700 /home/sandbox/.ssh
    echo "$SSH_PUBLIC_KEY" > /home/sandbox/.ssh/authorized_keys
    chmod 600 /home/sandbox/.ssh/authorized_keys
    chown -R sandbox:sandbox /home/sandbox/.ssh
    echo "SSH public key installed successfully"
fi

# Start SSH daemon
echo "Starting SSH daemon..."
exec /usr/sbin/sshd -D -e

