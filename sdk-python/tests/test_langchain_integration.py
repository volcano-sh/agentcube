# Copyright The Volcano Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import unittest
from unittest.mock import MagicMock, patch

from agentcube.integrations.langchain import AgentCubeSandbox
from agentcube.exceptions import CommandExecutionError

class TestAgentCubeSandbox(unittest.TestCase):
    """Test the LangChain Sandbox integration."""

    def setUp(self):
        self.mock_client = MagicMock()
        self.mock_client.session_id = "test-session-123"
        self.sandbox = AgentCubeSandbox(self.mock_client)

    def test_id_property(self):
        """Test the id property returns session_id."""
        self.assertEqual(self.sandbox.id, "test-session-123")

    def test_execute_success(self):
        """Test execute command success."""
        self.mock_client.execute_command.return_value = "hello world"

        response = self.sandbox.execute("echo hello world")

        self.assertEqual(response.output, "hello world")
        self.assertEqual(response.exit_code, 0)
        self.assertFalse(response.truncated)
        self.mock_client.execute_command.assert_called_once_with("echo hello world", timeout=None)

    def test_execute_failure(self):
        """Test execute command failure (CommandExecutionError)."""
        self.mock_client.execute_command.side_effect = CommandExecutionError(
            exit_code=127, stdout="some output", stderr="command not found", command="invalid"
        )

        response = self.sandbox.execute("invalid")

        # Expect combined output
        self.assertEqual(response.output, "some output\ncommand not found")
        self.assertEqual(response.exit_code, 127)
        self.mock_client.execute_command.assert_called_once()

    def test_upload_files(self):
        """Test uploading multiple files."""
        files = [
            ("test1.txt", b"hello"),
            ("test2.txt", b"world")
        ]

        responses = self.sandbox.upload_files(files)

        self.assertEqual(len(responses), 2)
        self.assertEqual(responses[0].path, "test1.txt")
        self.assertIsNone(responses[0].error)
        self.assertEqual(responses[1].path, "test2.txt")
        self.assertIsNone(responses[1].error)

        # Verify client calls
        self.assertEqual(self.mock_client.write_file.call_count, 2)
        self.mock_client.write_file.assert_any_call("hello", "test1.txt")
        self.mock_client.write_file.assert_any_call("world", "test2.txt")

    @patch("os.path.exists", return_value=True)
    @patch("os.remove")
    @patch("builtins.open", new_callable=MagicMock)
    @patch("tempfile.NamedTemporaryFile")
    def test_download_files(self, mock_tmpfile, mock_open, mock_remove, mock_exists):
        """Test downloading files."""
        # Setup mock temp file
        mock_tmpfile.return_value.__enter__.return_value.name = "/tmp/fake_path"

        # Mock file content
        file_content = b"file content"
        mock_open.return_value.__enter__.return_value.read.return_value = file_content

        paths = ["remote1.txt"]

        responses = self.sandbox.download_files(paths)

        self.assertEqual(len(responses), 1)
        self.assertEqual(responses[0].path, "remote1.txt")
        self.assertEqual(responses[0].content, file_content)
        self.assertIsNone(responses[0].error)

        self.mock_client.download_file.assert_called_once()
        self.assertTrue(mock_open.called)
        # Verify cleanup
        mock_exists.assert_called_with("/tmp/fake_path")
        mock_remove.assert_called_with("/tmp/fake_path")

if __name__ == "__main__":
    unittest.main()
