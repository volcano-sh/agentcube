#!/bin/bash
set -e

# Generate SSH host keys if they don't exist
if [ ! -f /etc/ssh/ssh_host_rsa_key ]; then
    sudo ssh-keygen -A
fi

# Start SSH daemon
echo "Starting SSH daemon..."
exec sudo /usr/sbin/sshd -D -e

