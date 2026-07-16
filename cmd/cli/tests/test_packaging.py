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

from pathlib import Path

from setuptools import find_packages


def test_packages_are_correctly_discovered():
    package_root = Path(__file__).resolve().parents[1]
    packages = set(find_packages(where=package_root, include=["agentcube*"]))

    expected_packages = {
        "agentcube",
        "agentcube.cli",
        "agentcube.runtime",
        "agentcube.operations",
        "agentcube.services",
        "agentcube.models",
    }

    assert expected_packages.issubset(packages)
