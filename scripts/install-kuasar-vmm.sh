#!/bin/bash
set -e

# --- Configuration Backup Directory (for containerd/crictl) ---
BACKUP_DIR="/tmp/kuasar_config_backup"
mkdir -p "${BACKUP_DIR}" 

# --- 0. Check and Install Dependencies (Assuming a Debian/Ubuntu-like system) ---
echo "--- 0. Installing necessary dependencies (git, wget, tar, unzip, make) ---"
sudo apt update
sudo apt install -y git wget tar unzip make

# --- 1. Install Cloud-Hypervisor VMM ---
echo "--- 1. Installing cloud-hypervisor v49.0 ---"
CLH_VERSION="v49.0"
CLH_URL="https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/${CLH_VERSION}/cloud-hypervisor"

wget ${CLH_URL}
chmod +x ./cloud-hypervisor
# Set cap_net_admin capability to allow network operations
sudo setcap cap_net_admin+ep ./cloud-hypervisor
sudo mv cloud-hypervisor /usr/local/bin/

# --- 2. Install Kuasar VMM Sandboxer and components ---
echo "--- 2. Installing Kuasar VMM Sandboxer and components ---"
KUASAR_VERSION="v1.0.1"
KUASAR_RELEASE="kuasar-${KUASAR_VERSION}-linux-amd64"

git clone https://github.com/kuasar-io/kuasar.git
cd kuasar
wget https://github.com/kuasar-io/kuasar/releases/download/${KUASAR_VERSION}/${KUASAR_RELEASE}.tar.gz
tar -zxvf ${KUASAR_RELEASE}.tar.gz

# Move binaries and image files
echo "Moving Kuasar binaries..."
mkdir -p ./bin
cd ${KUASAR_RELEASE}
cp vmm-sandboxer ../bin/vmm-sandboxer
cp vmlinux.bin ../bin/vmlinux.bin
cp kuasar.img ../bin/kuasar.img
cd ..
# Execute make install-vmm to copy necessary files to /usr/local/bin and /etc/kuasar
sudo make install-vmm

# --- 3. Install and Configure Containerd (with backup) ---
echo "--- 3. Installing and configuring containerd ---"
# Backup existing containerd config
if [ -f "/etc/containerd/config.toml" ]; then
    echo "--- Found existing /etc/containerd/config.toml. Backing up to ${BACKUP_DIR}/config.toml.bak ---"
    sudo mv /etc/containerd/config.toml "${BACKUP_DIR}/config.toml.bak"
else
    echo "--- No existing /etc/containerd/config.toml found. Skipping backup. ---"
fi

# Copy Kuasar's containerd binary
cd ${KUASAR_RELEASE}
chmod +x containerd
sudo cp ./containerd /usr/local/bin/
sudo mkdir -p /etc/containerd
# Copy Kuasar-provided config (adjusted for Kuasar)
sudo cp ./config.toml /etc/containerd/config.toml
cd ..

# --- 4. Install and Configure crictl (with backup) ---
echo "--- 4. Installing and configuring crictl ---"
CRICTL_VERSION="v1.30.0"
CRICTL_URL="https://github.com/kubernetes-sigs/cri-tools/releases/download/${CRICTL_VERSION}/crictl-${CRICTL_VERSION}-linux-amd64.tar.gz"

wget ${CRICTL_URL}
sudo tar zxvf crictl-${CRICTL_VERSION}-linux-amd64.tar.gz -C /usr/local/bin
rm -f crictl-${CRICTL_VERSION}-linux-amd64.tar.gz

# Backup existing crictl config
if [ -f "/etc/crictl.yaml" ]; then
    echo "--- Found existing /etc/crictl.yaml. Backing up to ${BACKUP_DIR}/crictl.yaml.bak ---"
    sudo mv /etc/crictl.yaml "${BACKUP_DIR}/crictl.yaml.bak"
else
    echo "--- No existing /etc/crictl.yaml found. Skipping backup. ---"
fi

# Create crictl config file
echo "Creating /etc/crictl.yaml configuration file..."
sudo tee /etc/crictl.yaml > /dev/null << EOF
runtime-endpoint: unix:///var/run/containerd/containerd.sock
image-endpoint: unix:///var/run/containerd/containerd.sock
timeout: 10
EOF

# --- 5. Install virtiofsd (for filesystem sharing) ---
echo "--- 5. Installing virtiofsd ---"
VIRTIOFSD_URL="https://gitlab.com/-/project/21523468/uploads/0298165d4cd2c73ca444a8c0f6a9ecc7/virtiofsd-v1.13.2.zip"
wget ${VIRTIOFSD_URL}
unzip virtiofsd-v1.13.2.zip
sudo mv target/x86_64-unknown-linux-musl/release/virtiofsd /usr/local/bin/
rm -rf virtiofsd-v1.13.2.zip target/x86_64-unknown-linux-musl/release/

echo "--- ✅ Kuasar + Cloud-Hypervisor Installation Completed Successfully! ---"
echo "Next step: Please manually start containerd and vmm-sandboxer, then run the example."