# Minimal Sandbox Image with SSH and Python
# This image supports multiple architectures: amd64, arm64, arm/v7
FROM python:3.11-alpine

# Install OpenSSH Server only
RUN apk add --no-cache openssh-server

# Create sandbox user and configure SSH
RUN adduser -D -s /bin/sh sandbox && \
    echo "sandbox:sandbox" | chpasswd && \
    mkdir -p /home/sandbox/.ssh && \
    chmod 700 /home/sandbox/.ssh && \
    chown -R sandbox:sandbox /home/sandbox/.ssh

# Configure SSH - minimal secure settings
RUN mkdir -p /var/run/sshd && \
    sed -i 's/#PermitRootLogin.*/PermitRootLogin no/' /etc/ssh/sshd_config && \
    sed -i 's/#PubkeyAuthentication.*/PubkeyAuthentication yes/' /etc/ssh/sshd_config && \
    sed -i 's/#PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config && \
    ssh-keygen -A

# Create workspace directory
RUN mkdir -p /workspace && \
    chown sandbox:sandbox /workspace

WORKDIR /workspace

# Copy entrypoint script
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 22

# Note: Container runs as root to allow sshd to start
# SSH connections will use the sandbox user

ENTRYPOINT ["/entrypoint.sh"]
CMD ["/usr/sbin/sshd", "-D", "-e"]

