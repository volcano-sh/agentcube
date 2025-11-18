\#!/bin/bash
set -e

# --- Configuration Backup Directory (Must match install script) ---
BACKUP_DIR="/tmp/kuasar_config_backup"

echo "--- ⚠️ WARNING: This script will delete Kuasar, Cloud-Hypervisor, and related configurations. Please confirm! ---"
read -r -p "Do you want to continue with uninstallation? (y/N): " response
if [[ "$response" =~ ^([yY][eE][sS]|[yY])$ ]]
then
    echo "Starting component-based uninstallation..."
else
    echo "Uninstallation cancelled."
    exit 0
fi

# Define paths and files to be removed
KUASAR_REPO_DIR="kuasar"
KUASAR_RELEASE_NAME="kuasar-v1.0.1-linux-amd64"

# --- 1. Global Service Shutdown ---
echo "--- 1. Stopping and removing services (Containerd/Kuasar) ---"
# Attempt to stop vmm-sandboxer service
if command -v systemctl &> /dev/null && systemctl list-unit-files | grep -q "kuasar-vmm.service"; then
    sudo systemctl stop kuasar-vmm
    sudo systemctl disable kuasar-vmm
fi
# Stop containerd (if still running)
if pgrep "containerd" &> /dev/null; then
    echo "Killing running containerd process..."
    sudo pkill -SIGTERM containerd || true
fi
sleep 2 # Wait briefly for processes to terminate


# --- 2. Component: Cloud-Hypervisor ---
echo "--- 2. Uninstalling Cloud-Hypervisor VMM ---"
sudo rm -f /usr/local/bin/cloud-hypervisor


# --- 3. Component: Kuasar VMM Sandboxer ---
echo "--- 3. Uninstalling Kuasar VMM Sandboxer and components ---"
# Delete binaries
sudo rm -f /usr/local/bin/vmm-sandboxer
sudo rm -f /var/lib/kuasar/vmlinux.bin
sudo rm -f /var/lib/kuasar/kuasar.img
sudo rm -rf /var/lib/kuasar

# Delete configs and runtime files
sudo rm -rf /run/kuasar-vmm
sudo rm -f /run/vmm-sandboxer.sock
sudo rm -f /sys/fs/cgroup/system.slice/kuasar-vmm.service || true
sudo rm -f /usr/lib/systemd/system/kuasar-vmm.service || true


# --- 4. Component: Containerd (Binary and Config Restoration) ---
echo "--- 4. Uninstalling Containerd and restoring configuration ---"
# Delete Kuasar's Containerd binary
sudo rm -f /usr/local/bin/containerd

# Restore containerd configuration
if [ -f "${BACKUP_DIR}/config.toml.bak" ]; then
    echo "--- Restoring backed-up /etc/containerd/config.toml ---"
    sudo rm -f /etc/containerd/config.toml # Remove Kuasar's config
    sudo mv "${BACKUP_DIR}/config.toml.bak" /etc/containerd/config.toml
else
    echo "--- No containerd backup found. Deleting Kuasar's config. ---"
    sudo rm -f /etc/containerd/config.toml
fi


# --- 5. Component: crictl (Binary and Config Restoration) ---
echo "--- 5. Uninstalling crictl and restoring configuration ---"
# Delete crictl binary
sudo rm -f /usr/local/bin/crictl

# Restore crictl configuration
if [ -f "${BACKUP_DIR}/crictl.yaml.bak" ]; then
    echo "--- Restoring backed-up /etc/crictl.yaml ---"
    sudo rm -f /etc/crictl.yaml # Remove Kuasar's config
    sudo mv "${BACKUP_DIR}/crictl.yaml.bak" /etc/crictl.yaml
else
    echo "--- No crictl backup found. Deleting Kuasar's config. ---"
    sudo rm -f /etc/crictl.yaml
fi


# --- 6. Component: virtiofsd ---
echo "--- 6. Uninstalling virtiofsd ---"
sudo rm -f /usr/local/bin/virtiofsd


# --- 7. Final Cleanup of Downloaded Files and Backup Directory ---
echo "--- 7. Cleaning up local downloaded files and directories ---"
# Remove backup directory
if [ -d "$BACKUP_DIR" ]; then
    echo "Removing temporary backup directory: $BACKUP_DIR"
    sudo rm -rf "$BACKUP_DIR"
fi


echo "--- ✅ Uninstallation Completed! ---"
echo "Note: You may need to restart your system or manually clean up residual data (e.g., Containerd storage)."