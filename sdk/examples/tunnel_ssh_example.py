"""
Example: Sessions (REST) + SSH/SFTP over HTTP CONNECT via the same API server.

Requirements:
- Environment variables:
  - SANDBOX_API_URL (e.g., https://api.sandbox.example.com/v1)
  - SANDBOX_API_TOKEN (Bearer token)
  - SANDBOX_SSH_USERNAME (username to use inside the sandbox)
  - Optional: SANDBOX_SSH_PASSWORD or SANDBOX_SSH_KEY (path to private key)

This demonstrates:
1) Creating a session via REST
2) Opening an HTTP CONNECT tunnel to /sessions/{id}
3) Running SSH commands and SFTP transfers through the tunnel
4) Deleting the session via REST
"""

import os
import tempfile
from sandbox_sessions_sdk import SessionsClient, SessionSSHClient, UnauthorizedError, SandboxConnectionError, SandboxOperationError
import paramiko


def main():
    api_url = os.environ.get("SANDBOX_API_URL", "http://localhost:8080/v1")
    token = os.environ.get("SANDBOX_API_TOKEN")
    username = os.environ.get("SANDBOX_SSH_USERNAME", "sandbox")
    password = os.environ.get("SANDBOX_SSH_PASSWORD")
    key_path = os.environ.get("SANDBOX_SSH_KEY")

    if not token:
        print("Missing SANDBOX_API_TOKEN; set it and rerun.")
        return

    # Optional: load a private key if provided
    pkey = None
    if key_path and os.path.exists(key_path):
        try:
            pkey = paramiko.RSAKey.from_private_key_file(key_path)
        except Exception:
            try:
                pkey = paramiko.Ed25519Key.from_private_key_file(key_path)
            except Exception as e:
                print(f"Failed to load private key: {e}")
                return

    session = None
    try:
        # 1) Create a session via REST
        with SessionsClient(api_url=api_url, bearer_token=token) as client:
            print("Creating session...")
            session = client.create_session(ttl=1800, image="python:3.11", metadata={"example": "tunnel"})
            print(f"✓ Created session: {session.session_id}")

        # 2) Use SSH/SFTP via HTTP CONNECT on the same API server
        print("Connecting SSH over HTTP CONNECT tunnel...")
        with SessionSSHClient(
            api_url=api_url,
            bearer_token=token,
            session_id=session.session_id,
            username=username,
            password=password,
            pkey=pkey,
            get_pty=False,
        ) as ssh:
            # Run a command
            res = ssh.run_command("echo hello && uname -a")
            print("STDOUT:\n" + res["stdout"])  
            if res["stderr"]:
                print("STDERR:\n" + res["stderr"]) 
            print(f"Exit: {res['exit_code']}")

            # Upload a temp file and then download it back
            with tempfile.NamedTemporaryFile("w", delete=False) as tmp:
                tmp.write("Hello from client via SFTP!\n")
                tmp_path = tmp.name

            remote_path = "demo.txt"
            ssh.upload_file(tmp_path, remote_path)
            print(f"Uploaded {tmp_path} -> {remote_path}")

            download_path = tmp_path + ".copy"
            ssh.download_file(remote_path, download_path)
            print(f"Downloaded {remote_path} -> {download_path}")

        # 3) Cleanup the session via REST
        with SessionsClient(api_url=api_url, bearer_token=token) as client:
            print("Deleting session...")
            client.delete_session(session.session_id)
            print("✓ Session deleted")

    except UnauthorizedError as e:
        print(f"Authentication error: {e.message}")
    except SandboxConnectionError as e:
        print(f"Connection error: {e.message}")
    except SandboxOperationError as e:
        print(f"Operation error: {e.message}")
    except Exception as e:
        print(f"Unexpected error: {e}")


if __name__ == "__main__":
    main()
