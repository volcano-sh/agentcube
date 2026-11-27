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

"""Check a locally built PicoD image through authenticated HTTP requests.

Build and run from the repository root (requires the SDK's cryptography dependency):
    podman build --build-arg TARGETARCH=amd64 -f docker/Dockerfile.picod -t localhost/picod-env-review .
    python3 sdk-python/scripts/e2e_picod_env_test.py --image localhost/picod-env-review

Only dummy credentials are used. The signing private key stays in this client.
No Router, Kubernetes cluster, or external credential service is involved.
"""

import argparse
import base64
from concurrent.futures import ThreadPoolExecutor
import json
import subprocess
import time
import urllib.error
import urllib.request
import uuid

from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import padding, rsa


def run(*command):
    return subprocess.run(command, check=True, capture_output=True, text=True).stdout.strip()


def check(condition, description):
    if not condition:
        raise AssertionError(description)


def signed_token(private_key):
    def encode(value):
        raw = json.dumps(value, separators=(",", ":")).encode()
        return base64.urlsafe_b64encode(raw).rstrip(b"=")

    message = encode({"alg": "RS256", "typ": "JWT"}) + b"." + encode({
        "iat": int(time.time()), "exp": int(time.time()) + 300,
    })
    signature = private_key.sign(message, padding.PKCS1v15(), hashes.SHA256())
    return (message + b"." + base64.urlsafe_b64encode(signature).rstrip(b"=")).decode()


def verify_image(engine, image):
    runtime = json.loads(run(
        engine, "run", "--rm", "--entrypoint", "python3", image, "-c",
        "import json, os, sys; print(json.dumps({"
        "'prefix':sys.base_prefix,'path':os.environ['PATH'],'version':sys.version}))",
    ))
    print("Image Python: " + runtime["version"], flush=True)
    workspace = "/tmp/picod-env-review"
    inherited = {
        "PATH": workspace + "/bin:" + runtime["path"],
        "HOME": workspace,
        "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8", "LC_CTYPE": "C.UTF-8", "TERM": "",
        "PYTHONHOME": runtime["prefix"], "PYTHONPATH": workspace + "/modules",
        "PYTHONUSERBASE": workspace + "/user", "PYTHONNOUSERSITE": "1",
        "PYTHONUNBUFFERED": "1", "PYTHONDONTWRITEBYTECODE": "1",
        "PYTHONIOENCODING": "utf-8", "PYTHONUTF8": "1",
    }
    blocked = {
        "AWS_SECRET_ACCESS_KEY": "dummy-aws-183", "PICOD_TEST_PARENT_SECRET": "dummy-parent-183",
        "PYTHON_API_KEY": "dummy-python-183", "KUBERNETES_SERVICE_HOST": "dummy-host-183",
        "HTTPS_PROXY": "http://proxy.invalid:3128", "SSL_CERT_FILE": "/dummy/cert",
        "REQUESTS_CA_BUNDLE": "/dummy/ca-bundle", "TMPDIR": "/dummy/tmp",
    }
    private_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    public_key = private_key.public_key().public_bytes(
        serialization.Encoding.PEM, serialization.PublicFormat.SubjectPublicKeyInfo,
    ).decode()
    name = "picod-env-183-" + uuid.uuid4().hex[:12]
    command = [engine, "run", "--detach", "--name", name, "--publish", "127.0.0.1::8080"]
    for key, value in dict(inherited, **blocked, PICOD_AUTH_PUBLIC_KEY=public_key).items():
        command.extend(["--env", key + "=" + value])
    command.extend([image, "--workspace=" + workspace])
    try:
        run(*command)
        port = run(engine, "port", name, "8080/tcp").splitlines()[0].rsplit(":", 1)[1]
        base_url = "http://127.0.0.1:" + port
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        deadline = time.monotonic() + 30
        while True:
            try:
                with opener.open(base_url + "/health", timeout=1) as response:
                    check(response.status == 200, "health endpoint status")
                break
            except (OSError, urllib.error.URLError):
                if time.monotonic() >= deadline:
                    raise RuntimeError("PicoD did not become healthy")
                time.sleep(0.1)

        omitted = object()

        def execute(command, env=omitted):
            payload = {"command": command, "timeout": "10s"}
            if env is not omitted:
                payload["env"] = env
            request = urllib.request.Request(
                base_url + "/api/execute", data=json.dumps(payload).encode(),
                headers={"Content-Type": "application/json", "Authorization": "Bearer " + signed_token(private_key)},
            )
            with opener.open(request, timeout=15) as response:
                result = json.load(response)
            check(result["exit_code"] == 0, "execution failed: " + result["stderr"])
            return result["stdout"].strip()

        names = list(inherited) + list(blocked) + ["PICOD_AUTH_PUBLIC_KEY", "REQUEST_ONLY", "EMPTY_REQUEST"]
        inspect = ["python3", "-c", "import json, os; print(json.dumps({"
                   "k:os.environ[k] for k in " + repr(names) + " if k in os.environ}))"]
        for label, env in [("omitted", omitted), ("null", None), ("empty", {}),
                           ("populated", {"REQUEST_ONLY": "dummy-request-183"})]:
            values = json.loads(execute(inspect, env))
            for key, value in inherited.items():
                check(key in values and values[key] == value, "inherited setting changed: " + key)
            for key in list(blocked) + ["PICOD_AUTH_PUBLIC_KEY"]:
                check(key not in values, "unexpected inherited name: " + key + " (" + label + ")")
            check(values.get("REQUEST_ONLY") == (None if env is omitted or not env else env["REQUEST_ONLY"]),
                  "request value missing")
            print("PASS environment " + label, flush=True)

        values = json.loads(execute(inspect, {"HOME": "request=home", "TERM": "", "EMPTY_REQUEST": "",
                                             "REQUEST_ONLY": "dummy-explicit-183"}))
        check(values["HOME"] == "request=home" and values["TERM"] == "" and values["EMPTY_REQUEST"] == "",
              "overrides and empty values")
        check(values["REQUEST_ONLY"] == "dummy-explicit-183", "explicit variable missing")
        values = json.loads(execute(inspect))
        check(values["HOME"] == inherited["HOME"] and "REQUEST_ONLY" not in values and "EMPTY_REQUEST" not in values,
              "request values automatically persisted")
        print("PASS overrides and consecutive requests", flush=True)

        def concurrent_request(index):
            expected = "concurrent-" + str(index)
            values = json.loads(execute(inspect, {"HOME": expected, "REQUEST_ONLY": expected}))
            check(values["HOME"] == expected and values["REQUEST_ONLY"] == expected, "concurrent request crossed")

        with ThreadPoolExecutor(max_workers=8) as pool:
            list(pool.map(concurrent_request, range(8)))
        check("REQUEST_ONLY" not in json.loads(execute(inspect)), "concurrent value automatically persisted")
        print("PASS concurrent requests", flush=True)

        parent_script = "#!/bin/sh\nprintf 'parent:%s\\n' \"$PATH\"\n"
        child_script = "#!/bin/sh\necho wrong-executable\n"
        setup = (
            "import pathlib; root=pathlib.Path(" + repr(workspace) + "); "
            "(root/'modules').mkdir(); (root/'bin').mkdir(); (root/'child-bin').mkdir(); "
            "(root/'modules'/'picod_env_fixture.py').write_text('VALUE = 183\\n'); "
            "script=root/'bin'/'picod-env-lookup'; "
            "script.write_text(" + repr(parent_script) + "); script.chmod(0o755); "
            "other=root/'child-bin'/'picod-env-lookup'; other.write_text(" + repr(child_script) + "); "
            "other.chmod(0o755)"
        )
        execute(["python3", "-c", setup])
        compatibility = (
            "import os, picod_env_fixture, site, subprocess, sys; "
            "assert picod_env_fixture.VALUE == 183; assert sys.prefix == os.environ['PYTHONHOME']; "
            "assert site.getuserbase() == os.environ['PYTHONUSERBASE']; "
            "assert sys.flags.no_user_site == 1; assert sys.flags.utf8_mode == 1; "
            "assert sys.dont_write_bytecode; assert sys.stdout.write_through; "
            "assert sys.stdout.encoding.lower() == 'utf-8'; "
            "assert subprocess.check_output(['sh','-c','printf subprocess-ok'], text=True) == 'subprocess-ok'; "
            "print('Unicode: नमस्ते 🌍')"
        )
        check(execute(["python3", "-c", compatibility]) == "Unicode: नमस्ते 🌍", "Python Unicode output")
        print("PASS Python import, PYTHONHOME, flags, encoding, and subprocess lookup", flush=True)
        child_path = workspace + "/child-bin"
        check(execute(["picod-env-lookup"], {"PATH": child_path}) == "parent:" + child_path,
              "bare command lookup must use parent PATH")
        print("PASS parent command lookup and child PATH override", flush=True)

        limitation = execute(["python3", "-c",
                              "import os\ntry:\n"
                              " data=open('/proc/%d/environ' % os.getppid(),'rb').read().split(b'\\0')\n"
                              " print(b'PICOD_TEST_PARENT_SECRET=dummy-parent-183' in data)\n"
                              "except OSError as error:\n print(type(error).__name__)"])
        print("LIMITATION dummy parent credential readable through /proc: " + limitation, flush=True)
        print("All supported-image HTTP environment checks passed.", flush=True)
    finally:
        # This unpredictable name belongs exclusively to this invocation.
        subprocess.run([engine, "rm", "--force", name], check=False, capture_output=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--container-tool", choices=("podman", "docker"), default="podman")
    parser.add_argument("--image", required=True, help="Image built from docker/Dockerfile.picod; never pushed")
    arguments = parser.parse_args()
    verify_image(arguments.container_tool, arguments.image)
