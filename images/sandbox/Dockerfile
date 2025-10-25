# Default Sandbox Image with SSH and Python
FROM python:3.11-slim

# Install OpenSSH Server and other utilities
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    openssh-server \
    sudo \
    curl \
    git \
    vim \
    && rm -rf /var/lib/apt/lists/*

# Create sandbox user
RUN useradd -m -s /bin/bash sandbox && \
    echo "sandbox:sandbox" | chpasswd && \
    usermod -aG sudo sandbox && \
    echo "sandbox ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers

# Configure SSH
RUN mkdir -p /var/run/sshd && \
    mkdir -p /home/sandbox/.ssh && \
    chmod 700 /home/sandbox/.ssh && \
    chown -R sandbox:sandbox /home/sandbox/.ssh

# SSH configuration
RUN sed -i 's/#PermitRootLogin prohibit-password/PermitRootLogin no/' /etc/ssh/sshd_config && \
    sed -i 's/#PasswordAuthentication yes/PasswordAuthentication yes/' /etc/ssh/sshd_config && \
    sed -i 's/#PubkeyAuthentication yes/PubkeyAuthentication yes/' /etc/ssh/sshd_config && \
    sed -i 's/#PermitEmptyPasswords no/PermitEmptyPasswords no/' /etc/ssh/sshd_config && \
    sed -i 's/UsePAM yes/UsePAM no/' /etc/ssh/sshd_config

# Create workspace directory
RUN mkdir -p /workspace && \
    chown sandbox:sandbox /workspace

# Set working directory
WORKDIR /workspace

# Install common Python packages
RUN pip install --no-cache-dir \
    requests \
    numpy \
    pandas \
    matplotlib \
    ipython

# Copy entrypoint script
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Expose SSH port
EXPOSE 22

# Switch to sandbox user for default operations
USER sandbox

# Set entrypoint
ENTRYPOINT ["/entrypoint.sh"]
CMD ["/usr/sbin/sshd", "-D", "-e"]

