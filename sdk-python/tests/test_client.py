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
        
        # 1. Create session with SSH key
        log.info("Step 1: Creating session with SSH key...")
        session_id = client.create_sandbox()
        log.info(f"✅ Session created: {session_id}\n")

        # 2. Establish tunnel
        log.info("Step 2: Establishing HTTP tunnel...")
        client.establish_tunnel(session_id)
        log.info("✅ Tunnel established\n")

        # 1. get session info
        log.info("Step 3: Retrieving session info...")
        session_info = client.get_sandbox(session_id)
        log.info(f"   Session Info: {json.dumps(session_info, indent=2)}\n")
        
        # 4. get session list
        log.info("Step 4: Retrieving session list...")
        session_list = client.list_sandboxes()
        log.info(f"   Session List: {json.dumps(session_list, indent=2)}\n")

        # 5. Delete session
        log.info("\nStep 5: Deleting session...")
        if client.delete_sandbox(session_id):
            log.info(f"✅ Session {session_id} deleted\n")
        else:
            log.error(f"❌ Failed to delete session {session_id}\n")
        
        
    except Exception as e:
        log.error(f"\n❌ Error: {str(e)}")
    finally:
        if 'client' in locals():
            client.cleanup()

if __name__ == "__main__":
    main()