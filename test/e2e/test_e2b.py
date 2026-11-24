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

"""End-to-end tests using the unmodified official e2b & e2b-code-interpreter SDKs."""

import os
import subprocess
import sys
import time
from datetime import timedelta

import httpx
from e2b.exceptions import NotFoundException, SandboxException
from e2b_code_interpreter import Sandbox


def require_env(name: str) -> str:
    val = os.environ.get(name, "").strip()
    if not val:
        raise RuntimeError(f"missing required environment variable: {name}")
    return val


def verify_k8s_and_redis_cleaned(namespace: str, session_id: str) -> None:
    """Poll briefly for K8s Sandbox/SandboxClaim and Redis key removal after kill()."""
    deadline = time.time() + 45
    while time.time() < deadline:
        claims = subprocess.run(
            [
                "kubectl",
                "-n",
                namespace,
                "get",
                "sandboxclaims",
                "-l",
                f"runtime.agentcube.io/session-id={session_id}",
                "-o",
                "name",
            ],
            capture_output=True,
            text=True,
            check=False,
        )
        sandboxes = subprocess.run(
            [
                "kubectl",
                "-n",
                namespace,
                "get",
                "sandboxes.agents.x-k8s.io",
                "-l",
                f"runtime.agentcube.io/session-id={session_id}",
                "-o",
                "name",
            ],
            capture_output=True,
            text=True,
            check=False,
        )
        redis_res = subprocess.run(
            [
                "kubectl",
                "-n",
                namespace,
                "exec",
                "deployment/redis",
                "--",
                "redis-cli",
                "EXISTS",
                f"session:{session_id}",
            ],
            capture_output=True,
            text=True,
            check=False,
        )
        if (
            not claims.stdout.strip()
            and not sandboxes.stdout.strip()
            and redis_res.stdout.strip() == "0"
        ):
            return
        time.sleep(1)
    raise AssertionError(f"K8s or Redis resources leaked for session {session_id}")


def run_tests() -> None:
    domain = require_env("E2B_DOMAIN")
    api_url = require_env("E2B_API_URL")
    key_a = require_env("E2B_API_KEY")
    key_b = require_env("E2B_API_KEY_TENANT_B")
    namespace = os.environ.get("E2B_TEST_NAMESPACE", "agentcube")
    ca_cert = os.environ.get("SSL_CERT_FILE") or True

    common_opts = {
        "api_key": key_a,
        "domain": domain,
        "api_url": api_url,
    }

    print("1. Creating WarmPool sandbox via alias 'code-interpreter-v1'...")
    sbx = Sandbox.create(
        template="code-interpreter-v1",
        envs={"AGENTCUBE_TEST": "ok"},
        timeout=120,
        **common_opts,
    )
    sid = sbx.sandbox_id
    print(f"   created sandbox_id={sid}, domain={sbx.sandbox_domain}")

    try:
        print("2. Verifying commands.run + streaming stdout callback...")
        streamed = []
        cmd_res = sbx.commands.run(
            "echo $AGENTCUBE_TEST && uname -m",
            on_stdout=lambda data: streamed.append(data),
        )
        assert cmd_res.exit_code == 0, f"unexpected exit code: {cmd_res}"
        assert "ok" in cmd_res.stdout, f"missing env var output: {cmd_res.stdout!r}"
        assert any("ok" in chunk for chunk in streamed), f"streaming callback empty: {streamed}"

        print("3. Verifying files.write, read, list, and cross-runtime file visibility...")
        sbx.files.write("/tmp/hello.txt", "from-agentcube")
        content = sbx.files.read("/tmp/hello.txt")
        assert content == "from-agentcube", f"unexpected file content: {content!r}"
        entries = sbx.files.list("/tmp")
        assert any(e.name == "hello.txt" for e in entries), f"hello.txt not listed: {entries}"

        print("4. Verifying stateful run_code, env_vars on 49999, and remove_code_context...")
        exec1 = sbx.run_code("x = 41")
        assert exec1.error is None, f"turn 1 error: {exec1.error}"

        exec2 = sbx.run_code(
            "import os\n"
            "assert open('/tmp/hello.txt').read() == 'from-agentcube'\n"
            "assert os.environ.get('AGENTCUBE_TEST') == 'ok'\n"
            "x + 1\n"
        )
        assert exec2.error is None, f"turn 2 error: {exec2.error}"
        assert exec2.text == "42", f"expected stateful result '42', got {exec2.text!r} ({exec2.results})"

        ctx = sbx.create_code_context(language="python")
        ctx_exec = sbx.run_code("ctx_val = 99; ctx_val", context=ctx)
        assert ctx_exec.error is None and ctx_exec.text == "99", f"context exec failed: {ctx_exec}"
        sbx.remove_code_context(ctx)

        print("5. Verifying get_host(8000) custom port proxy...")
        http_srv = sbx.commands.run(
            "python3 -m http.server 8000 --directory /tmp",
            background=True,
        )
        try:
            host_url = f"https://{sbx.get_host(8000)}/hello.txt"
            fetched = None
            last_probe = None
            for _ in range(20):
                try:
                    resp = httpx.get(
                        host_url,
                        verify=ca_cert,
                        timeout=2.0,
                        trust_env=False,
                    )
                    if resp.status_code == 200:
                        fetched = resp.text
                        break
                    last_probe = f"status={resp.status_code} body={resp.text!r}"
                except httpx.HTTPError as exc:
                    last_probe = f"{type(exc).__name__}: {exc}"
                time.sleep(0.3)
            assert fetched == "from-agentcube", (
                f"get_host probe failed: {fetched!r} (url={host_url}, last={last_probe})"
            )
        finally:
            http_srv.kill()

        sbx.files.remove("/tmp/hello.txt")
        try:
            sbx.files.read("/tmp/hello.txt")
            raise AssertionError("expected /tmp/hello.txt to be removed")
        except SandboxException:
            pass

        print("6. Verifying list, get_info, set_timeout, and connect semantics...")
        listed = Sandbox.list(**common_opts).next_items()
        assert any(item.sandbox_id == sid for item in listed), f"{sid} missing from Sandbox.list()"

        info_before = sbx.get_info()
        sbx.set_timeout(240)
        info_after_set = sbx.get_info()
        assert info_after_set.end_at > info_before.end_at, "set_timeout(240) did not advance end_at"

        # connect(timeout=60) must NOT shorten the 240s deadline.
        reconnected = Sandbox.connect(sid, timeout=60, **common_opts)
        info_after_connect = reconnected.get_info()
        assert (
            info_after_connect.end_at >= info_after_set.end_at - timedelta(seconds=1)
        ), "connect shortened end_at"
        assert abs((info_after_connect.end_at - info_after_set.end_at).total_seconds()) < 5

        print("7. Verifying direct /init through proxy is blocked (403)...")
        init_resp = httpx.post(
            f"https://{sbx.get_host(49983)}/init",
            json={"envVars": {"HIJACK": "1"}},
            verify=ca_cert,
            timeout=5.0,
            trust_env=False,
        )
        assert init_resp.status_code == 403, f"expected 403 for /init via proxy, got {init_resp.status_code}"

        # Native Router invocation path must reject E2B sessions (404) rather than bypassing E2B proxy controls.
        port_part = domain.rsplit(":", 1)[-1] if ":" in domain else "443"
        native_resp = httpx.post(
            f"https://localhost:{port_part}/v1/namespaces/{namespace}/code-interpreters/e2b-code-interpreter/invocations/init",
            headers={"x-agentcube-session-id": sid},
            verify=ca_cert,
            timeout=5.0,
            trust_env=False,
        )
        assert native_resp.status_code == 404, (
            f"expected 404 for E2B session via native Router invoke, got {native_resp.status_code}"
        )

        print("8. Verifying cross-tenant isolation...")
        tenant_b_opts = {"api_key": key_b, "domain": domain, "api_url": api_url}
        try:
            Sandbox.get_info(sid, **tenant_b_opts)
            raise AssertionError("tenant B unexpectedly read tenant A sandbox")
        except NotFoundException:
            pass

        try:
            Sandbox.set_timeout(sid, 60, **tenant_b_opts)
            raise AssertionError("tenant B unexpectedly extended tenant A sandbox")
        except NotFoundException:
            pass

        assert Sandbox.kill(sid, **tenant_b_opts) is False, "tenant B unexpectedly killed tenant A sandbox"
    finally:
        print("9. Killing WarmPool sandbox and verifying K8s/Redis cleanup...")
        killed = sbx.kill()
        assert killed is True, "expected kill() to return True"

    try:
        Sandbox.get_info(sid, **common_opts)
        raise AssertionError("expected NotFoundException after kill()")
    except NotFoundException:
        pass

    verify_k8s_and_redis_cleaned(namespace, sid)

    print("10. Verifying cold-start template (warmPoolSize=0)...")
    cold = Sandbox.create(
        template="e2b-code-interpreter-cold",
        envs={"COLD_ENV": "yes"},
        timeout=120,
        **common_opts,
    )
    cold_sid = cold.sandbox_id
    try:
        cold_res = cold.commands.run("echo $COLD_ENV")
        assert cold_res.exit_code == 0 and cold_res.stdout.strip() == "yes", f"cold env mismatch: {cold_res}"
    finally:
        assert cold.kill() is True
        verify_k8s_and_redis_cleaned(namespace, cold_sid)

    print("All official E2B SDK E2E checks passed!")


if __name__ == "__main__":
    try:
        run_tests()
    except Exception as exc:
        print(f"E2E test failed: {exc}", file=sys.stderr)
        raise
