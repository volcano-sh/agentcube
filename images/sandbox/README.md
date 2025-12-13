# Sandbox Default Image

This is the default sandbox image used by workloadmanager. It includes:

- **Python 3.11**: Full Python environment with common packages
- **SSH Server**: OpenSSH server for remote access
- **Development Tools**: git, vim, curl, etc.

## Features

- **User**: `sandbox` (non-root user with sudo privileges)
- **Password**: `sandbox` (default, should be changed in production)
- **SSH Port**: 22
- **Working Directory**: `/workspace`
- **Pre-installed Python Packages**:
  - requests
  - numpy
  - pandas
  - matplotlib
  - ipython

## Building

```bash
# Build the image
make sandbox-build

# Build and push to registry
make sandbox-push IMAGE_REGISTRY=your-registry.com
```

## Usage

### Local Testing

```bash
# Run the container
docker run -d -p 2222:22 --name sandbox-test sandbox:latest

# Connect via SSH
ssh -p 2222 sandbox@localhost
# Password: sandbox
```

### In Kubernetes

The image is automatically used when creating sandboxes through workloadmanager:

```bash
curl -X POST http://localhost:8080/v1/sessions \
  -H "Authorization: Bearer token" \
  -H "Content-Type: application/json" \
  -d '{
    "ttl": 3600,
    "image": "sandbox:latest"
  }'
```

## Security Considerations

**⚠️ Important for Production:**

1. **Change Default Password**: The default password `sandbox` should be changed
2. **Use SSH Keys**: Configure SSH key authentication instead of passwords
3. **Restrict Sudo**: Remove or restrict sudo access as needed
4. **Network Policies**: Use Kubernetes network policies to restrict access
5. **Resource Limits**: Set appropriate CPU and memory limits

## Customization

### Adding More Python Packages

Edit the `Dockerfile` and add packages to the `pip install` command:

```dockerfile
RUN pip install --no-cache-dir \
    requests \
    numpy \
    your-package-here
```

### Changing Default User

Edit the user creation in `Dockerfile`:

```dockerfile
RUN useradd -m -s /bin/bash myuser && \
    echo "myuser:mypassword" | chpasswd
```

### Adding SSH Keys

Create a file `authorized_keys` and add it to the image:

```dockerfile
COPY authorized_keys /home/sandbox/.ssh/authorized_keys
RUN chmod 600 /home/sandbox/.ssh/authorized_keys && \
    chown sandbox:sandbox /home/sandbox/.ssh/authorized_keys
```

## Environment Variables

You can customize behavior with environment variables:

- `SANDBOX_USER`: Username (default: sandbox)
- `SANDBOX_PASSWORD`: User password (set at runtime)

## Troubleshooting

### SSH Connection Refused

Check if sshd is running:
```bash
docker exec sandbox-test ps aux | grep sshd
```

### Permission Denied

Verify SSH configuration:
```bash
docker exec sandbox-test cat /etc/ssh/sshd_config
```

### Python Package Issues

Install additional packages:
```bash
docker exec sandbox-test pip install package-name
```

