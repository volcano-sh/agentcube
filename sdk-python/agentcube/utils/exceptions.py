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

class SandboxError(Exception):
    """Base exception for all sandbox operations"""
    pass

class SandboxNotFoundError(SandboxError):
    """Raised when sandbox does not exist"""
    pass

class SandboxNotReadyError(SandboxError):
    """Raised when sandbox is not in 'running' state"""
    pass

class OperationTimeoutError(SandboxError):
    """Raised when operation exceeds timeout"""
    pass

class ProviderError(SandboxError):
    """Raised when agentcube-apiserver returns an error"""
    pass