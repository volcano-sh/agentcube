"""
Test the refactored Sandbox and CodeInterpreterClient classes
"""
import unittest
from unittest.mock import Mock, patch, MagicMock
from agentcube import Sandbox, CodeInterpreterClient
from agentcube.sandbox import SandboxStatus


class TestSandboxBase(unittest.TestCase):
    """Test base Sandbox class for lifecycle management"""
    
    @patch('agentcube.sandbox.SandboxClient')
    def test_sandbox_initialization(self, mock_client_class):
        """Test that Sandbox can be initialized with basic lifecycle management"""
        # Setup mock
        mock_client = Mock()
        mock_client.create_sandbox.return_value = "test-sandbox-id"
        mock_client_class.return_value = mock_client
        
        # Create sandbox
        sandbox = Sandbox(ttl=3600, image="test-image:latest")
        
        # Verify
        self.assertEqual(sandbox.id, "test-sandbox-id")
        self.assertEqual(sandbox.ttl, 3600)
        self.assertEqual(sandbox.image, "test-image:latest")
        mock_client.create_sandbox.assert_called_once()
    
    @patch('agentcube.sandbox.SandboxClient')
    def test_sandbox_lifecycle_methods(self, mock_client_class):
        """Test that Sandbox has lifecycle management methods"""
        mock_client = Mock()
        mock_client.create_sandbox.return_value = "test-sandbox-id"
        mock_client.get_sandbox.return_value = {"status": SandboxStatus.RUNNING.value}
        mock_client_class.return_value = mock_client
        
        sandbox = Sandbox()
        
        # Test lifecycle methods exist
        self.assertTrue(hasattr(sandbox, 'is_running'))
        self.assertTrue(hasattr(sandbox, 'get_info'))
        self.assertTrue(hasattr(sandbox, 'list_sandboxes'))
        self.assertTrue(hasattr(sandbox, 'stop'))
        self.assertTrue(hasattr(sandbox, 'cleanup'))
        
        # Test is_running
        self.assertTrue(sandbox.is_running())
        mock_client.get_sandbox.assert_called_with("test-sandbox-id")
    
    @patch('agentcube.sandbox.SandboxClient')
    def test_sandbox_no_dataplane_methods(self, mock_client_class):
        """Test that base Sandbox does not have dataplane methods"""
        mock_client = Mock()
        mock_client.create_sandbox.return_value = "test-sandbox-id"
        mock_client_class.return_value = mock_client
        
        sandbox = Sandbox()
        
        # Verify dataplane methods are not directly available
        self.assertFalse(hasattr(sandbox, 'execute_command'))
        self.assertFalse(hasattr(sandbox, 'run_code'))
        self.assertFalse(hasattr(sandbox, 'write_file'))


class TestCodeInterpreterClient(unittest.TestCase):
    """Test CodeInterpreterClient class with dataplane operations"""
    
    @patch('agentcube.sandbox.SandboxSSHClient')
    @patch('agentcube.sandbox.SandboxClient')
    def test_code_interpreter_initialization(self, mock_client_class, mock_ssh_class):
        """Test that CodeInterpreterClient initializes with SSH connection"""
        # Setup mocks
        mock_client = Mock()
        mock_client.create_sandbox.return_value = "test-sandbox-id"
        mock_client.establish_tunnel.return_value = Mock()
        mock_client_class.return_value = mock_client
        
        mock_ssh_class.generate_ssh_key_pair.return_value = ("public_key", "private_key")
        
        # Create code interpreter
        code_interpreter = CodeInterpreterClient(ttl=3600, image="test-image:latest")
        
        # Verify
        self.assertEqual(code_interpreter.id, "test-sandbox-id")
        mock_ssh_class.generate_ssh_key_pair.assert_called_once()
        mock_client.establish_tunnel.assert_called_once_with("test-sandbox-id")
    
    @patch('agentcube.sandbox.SandboxSSHClient')
    @patch('agentcube.sandbox.SandboxClient')
    def test_code_interpreter_inherits_from_sandbox(self, mock_client_class, mock_ssh_class):
        """Test that CodeInterpreterClient inherits from Sandbox"""
        # Setup mocks
        mock_client = Mock()
        mock_client.create_sandbox.return_value = "test-sandbox-id"
        mock_client.establish_tunnel.return_value = Mock()
        mock_client_class.return_value = mock_client
        mock_ssh_class.generate_ssh_key_pair.return_value = ("public_key", "private_key")
        
        code_interpreter = CodeInterpreterClient()
        
        # Verify inheritance
        self.assertIsInstance(code_interpreter, Sandbox)
        self.assertIsInstance(code_interpreter, CodeInterpreterClient)
    
    @patch('agentcube.sandbox.SandboxSSHClient')
    @patch('agentcube.sandbox.SandboxClient')
    def test_code_interpreter_has_dataplane_methods(self, mock_client_class, mock_ssh_class):
        """Test that CodeInterpreterClient has dataplane methods"""
        # Setup mocks
        mock_client = Mock()
        mock_client.create_sandbox.return_value = "test-sandbox-id"
        mock_client.establish_tunnel.return_value = Mock()
        mock_client.get_sandbox.return_value = {"status": SandboxStatus.RUNNING.value}
        mock_client_class.return_value = mock_client
        
        mock_ssh_class.generate_ssh_key_pair.return_value = ("public_key", "private_key")
        mock_executor = Mock()
        mock_ssh_class.return_value = mock_executor
        
        code_interpreter = CodeInterpreterClient()
        
        # Verify dataplane methods exist
        self.assertTrue(hasattr(code_interpreter, 'execute_command'))
        self.assertTrue(hasattr(code_interpreter, 'execute_commands'))
        self.assertTrue(hasattr(code_interpreter, 'run_code'))
        self.assertTrue(hasattr(code_interpreter, 'write_file'))
        self.assertTrue(hasattr(code_interpreter, 'upload_file'))
        self.assertTrue(hasattr(code_interpreter, 'download_file'))
        
        # Test execute_command
        mock_executor.execute_command.return_value = "test output"
        output = code_interpreter.execute_command("test command")
        self.assertEqual(output, "test output")
        mock_executor.execute_command.assert_called_once_with("test command")
    
    @patch('agentcube.sandbox.SandboxSSHClient')
    @patch('agentcube.sandbox.SandboxClient')
    def test_code_interpreter_has_lifecycle_methods(self, mock_client_class, mock_ssh_class):
        """Test that CodeInterpreterClient inherits lifecycle methods from Sandbox"""
        # Setup mocks
        mock_client = Mock()
        mock_client.create_sandbox.return_value = "test-sandbox-id"
        mock_client.establish_tunnel.return_value = Mock()
        mock_client.get_sandbox.return_value = {"status": SandboxStatus.RUNNING.value}
        mock_client_class.return_value = mock_client
        
        mock_ssh_class.generate_ssh_key_pair.return_value = ("public_key", "private_key")
        
        code_interpreter = CodeInterpreterClient()
        
        # Verify lifecycle methods exist (inherited from Sandbox)
        self.assertTrue(hasattr(code_interpreter, 'is_running'))
        self.assertTrue(hasattr(code_interpreter, 'get_info'))
        self.assertTrue(hasattr(code_interpreter, 'list_sandboxes'))
        self.assertTrue(hasattr(code_interpreter, 'stop'))


if __name__ == '__main__':
    unittest.main()
