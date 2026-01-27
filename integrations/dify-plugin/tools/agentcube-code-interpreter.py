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

import os
from collections.abc import Generator
from typing import Any

from dify_plugin import Tool
from dify_plugin.entities.tool import ToolInvokeMessage
from agentcube import CodeInterpreterClient

class AgentcubeCodeInterpreterTool(Tool):
    def _invoke(self, tool_parameters: dict[str, Any]) -> Generator[ToolInvokeMessage]:
        result = self.execute(**tool_parameters)
        yield self.create_json_message(result)
        

    def execute(self, router_url=None, workload_manager_url=None,language=None, code_interpreter_id=None, session_id=None, code=None, command=None, session_reuse=False, **kwargs):
        error_msg = ""
        results = []
        ci_client = None
        try:
            client_kwargs = {
                "router_url": router_url,
                "workload_manager_url": workload_manager_url
            }
            if code_interpreter_id:
                client_kwargs["name"] = code_interpreter_id
            if session_id:
                client_kwargs["session_id"] = session_id

            ci_client = CodeInterpreterClient(**client_kwargs)

            if command:
                command_result = ci_client.execute_command(command)
                results.append({"type": "command", "result": command_result})
            
            if language and code:
                code_result = ci_client.run_code(language, code)
                results.append({"type": "code", "result": code_result})
            
            if not command and not code:
                raise ValueError("Either command or code must be provided")
        except Exception as e:
            error_msg = str(e)
        finally:
            if ci_client and not session_reuse:
                ci_client.stop()

        if error_msg:
            result = {"status": "error", "reason": error_msg}
        else:
            result = {
                "status": "success", 
                "session_id": ci_client.session_id, 
                "code_interpreter_id": code_interpreter_id,
                "results": results
            }
        
        return result
    
