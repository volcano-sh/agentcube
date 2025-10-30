import logging
import json

# Import the PicoClient from the pico package
from agentcube.clients.client import SandboxClient

def main():
    # Configure logging
    logging.basicConfig(level=logging.INFO, format='%(message)s')
    log = logging.getLogger(__name__)
    
    try:
        # Initialize Pico client
        client = SandboxClient()
        
        log.info("===========================================")
        log.info("SSH Key-based Authentication Test")
        log.info("===========================================\n")
        
        # 1. Create sandbox with SSH key
        log.info("Step 1: Creating sandbox with SSH key...")
        sandbox_id = client.create_sandbox()
        log.info(f"✅ sandbox created: {sandbox_id}\n")

        # 2. Establish tunnel
        log.info("Step 2: Establishing HTTP tunnel...")
        client.establish_tunnel(sandbox_id)
        log.info("✅ Tunnel established\n")

        # 1. get sandbox info
        log.info("Step 3: Retrieving sandbox info...")
        sandbox_info = client.get_sandbox(sandbox_id)
        log.info(f"   sandbox Info: {json.dumps(sandbox_info, indent=2)}\n")
        
        # 4. get sandbox list
        log.info("Step 4: Retrieving sandbox list...")
        sandbox_list = client.list_sandboxes()
        log.info(f"   sandbox List: {json.dumps(sandbox_list, indent=2)}\n")

        # 5. Delete sandbox
        log.info("\nStep 5: Deleting sandbox...")
        if client.delete_sandbox(sandbox_id):
            log.info(f"✅ sandbox {sandbox_id} deleted\n")
        else:
            log.error(f"❌ Failed to delete sandbox {sandbox_id}\n")
        
        
    except Exception as e:
        log.error(f"\n❌ Error: {str(e)}")
    finally:
        if 'client' in locals():
            client.cleanup()

if __name__ == "__main__":
    main()