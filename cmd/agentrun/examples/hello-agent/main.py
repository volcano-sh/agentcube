#!/usr/bin/env python3
"""
Hello Agent - A simple example AI agent.

This agent provides a basic HTTP API for greeting users.
"""

import os
from http.server import HTTPServer, BaseHTTPRequestHandler
import json
import urllib.parse
from typing import Dict, Any


class HelloAgentHandler(BaseHTTPRequestHandler):
    """HTTP handler for the Hello Agent."""

    def do_GET(self):
        """Handle GET requests."""
        if self.path == '/health':
            self._send_json_response({"status": "healthy", "agent": "hello-agent"})
        elif self.path == '/':
            self._send_json_response({
                "message": "Hello from Hello Agent!",
                "endpoints": [
                    "GET /health - Health check",
                    "POST /greet - Greet someone",
                    "GET / - Agent information"
                ]
            })
        else:
            self._send_error(404, "Endpoint not found")

    def do_POST(self):
        """Handle POST requests."""
        if self.path == '/greet':
            self._handle_greet()
        elif self.path == '/':
            self._handle_invoke()
        else:
            self._send_error(404, "Endpoint not found")

    def _handle_greet(self):
        """Handle greeting requests."""
        try:
            content_length = int(self.headers['Content-Length'])
            post_data = self.rfile.read(content_length)
            data = json.loads(post_data.decode('utf-8'))

            name = data.get('name', 'World')
            language = data.get('language', 'en')

            greetings = {
                'en': f"Hello, {name}!",
                'es': f"¡Hola, {name}!",
                'fr': f"Bonjour, {name}!",
                'zh': f"你好, {name}!",
                'ja': f"こんにちは, {name}!"
            }

            greeting = greetings.get(language, greetings['en'])

            response = {
                "greeting": greeting,
                "name": name,
                "language": language,
                "agent": "hello-agent"
            }

            self._send_json_response(response)

        except (json.JSONDecodeError, KeyError) as e:
            self._send_error(400, f"Invalid request: {str(e)}")

    def _handle_invoke(self):
        """Handle general invocation requests."""
        try:
            content_length = int(self.headers['Content-Length'])
            post_data = self.rfile.read(content_length)
            data = json.loads(post_data.decode('utf-8'))

            # Handle different types of prompts
            prompt = data.get('prompt', '')
            if not prompt:
                self._send_error(400, "Prompt is required")
                return

            response_data = {
                "response": f"Hello Agent received: {prompt}",
                "agent": "hello-agent",
                "timestamp": self._get_timestamp(),
                "original_prompt": prompt
            }

            self._send_json_response(response_data)

        except (json.JSONDecodeError, KeyError) as e:
            self._send_error(400, f"Invalid request: {str(e)}")

    def _send_json_response(self, data: Dict[str, Any], status_code: int = 200):
        """Send JSON response."""
        self.send_response(status_code)
        self.send_header('Content-type', 'application/json')
        self.send_header('Access-Control-Allow-Origin', '*')
        self.end_headers()

        response = json.dumps(data, indent=2)
        self.wfile.write(response.encode('utf-8'))

    def _send_error(self, status_code: int, message: str):
        """Send error response."""
        self.send_response(status_code)
        self.send_header('Content-type', 'application/json')
        self.end_headers()

        error_response = json.dumps({
            "error": message,
            "status_code": status_code
        }, indent=2)

        self.wfile.write(error_response.encode('utf-8'))

    def _get_timestamp(self) -> str:
        """Get current timestamp."""
        from datetime import datetime
        return datetime.now().isoformat()

    def log_message(self, format, *args):
        """Override log_message to reduce noise."""
        pass


def main():
    """Main function to run the Hello Agent."""
    port = int(os.environ.get('PORT', 8080))

    print(f"🚀 Starting Hello Agent on port {port}")
    print(f"📡 Health check: http://localhost:{port}/health")
    print(f"👋 Greet endpoint: http://localhost:{port}/greet")
    print(f"🎯 Agent endpoint: http://localhost:{port}/")

    server_address = ('', port)
    httpd = HTTPServer(server_address, HelloAgentHandler)

    try:
        print(f"✅ Hello Agent is running on port {port}")
        httpd.serve_forever()
    except KeyboardInterrupt:
        print(f"\n🛑 Shutting down Hello Agent")
        httpd.server_close()


if __name__ == '__main__':
    main()