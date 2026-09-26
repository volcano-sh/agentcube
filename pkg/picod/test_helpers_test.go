/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package picod

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const testHelperProcessEnv = "GO_WANT_PICOD_HELPER_PROCESS"

func init() {
	if os.Getenv(testHelperProcessEnv) == "1" {
		picodHelperProcessMain()
		os.Exit(0)
	}
}

func testCommand(args ...string) []string {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	command := []string{exe, "--"}
	return append(command, args...)
}

func requireSymlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		if isWindowsSymlinkPrivilegeError(err) {
			t.Skipf("creating symlinks requires privileges on Windows: %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}
}

func isWindowsSymlinkPrivilegeError(err error) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		var errno syscall.Errno
		if errors.As(linkErr.Err, &errno) {
			return errno == syscall.Errno(1314)
		}
	}
	return false
}

func picodHelperProcessArgs() []string {
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "missing helper separator")
		os.Exit(2)
	}
	args = args[1:]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "missing helper command")
		os.Exit(2)
	}
	return args
}

func picodHelperProcessMain() {
	args := picodHelperProcessArgs()
	switch args[0] {
	case "echo":
		fmt.Println(strings.Join(args[1:], " "))
	case "env":
		helperPrintEnv(args)
	case "echo-env":
		helperEchoEnv(args)
	case "exit":
		os.Exit(helperExitCode(args))
	case "pwd":
		helperPrintWorkingDirectory()
	case "sleep":
		helperSleep(args)
	case "stderr-exit":
		helperStderrExit(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown helper command: %s\n", args[0])
		os.Exit(2)
	}
}

func helperPrintEnv(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "env helper requires variable name")
		os.Exit(2)
	}
	fmt.Println(os.Getenv(args[1]))
}

func helperEchoEnv(args []string) {
	values := make([]string, 0, len(args)-1)
	for _, key := range args[1:] {
		values = append(values, os.Getenv(key))
	}
	fmt.Println(strings.Join(values, " "))
}

func helperExitCode(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "exit helper requires code")
		os.Exit(2)
	}
	code, err := strconv.Atoi(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid exit code: %v\n", err)
		os.Exit(2)
	}
	return code
}

func helperPrintWorkingDirectory() {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "getwd: %v\n", err)
		os.Exit(2)
	}
	fmt.Println(wd)
}

func helperSleep(args []string) {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "sleep helper requires duration")
		os.Exit(2)
	}
	duration, err := time.ParseDuration(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid duration: %v\n", err)
		os.Exit(2)
	}
	time.Sleep(duration)
}

func helperStderrExit(args []string) {
	if len(args) != 3 {
		fmt.Fprintln(os.Stderr, "stderr-exit helper requires message and code")
		os.Exit(2)
	}
	fmt.Fprintln(os.Stderr, args[1])
	os.Exit(helperExitCode([]string{"exit", args[2]}))
}
