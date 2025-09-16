/*
Copyright 2025 Psiphon Inc.

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
package udsipc

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"testing"
)

// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// LocalPath returns a local path that can be used for Unix-domain
// protocol testing.
// This function is copied from golang.org/x/net/nettest package.
func LocalPath() (string, error) {
	dir := ""
	if runtime.GOOS == "darwin" {
		dir = "/tmp"
	}
	f, err := os.CreateTemp(dir, "go-nettest")
	if err != nil {
		return "", err
	}
	path := f.Name()
	f.Close()
	os.Remove(path)
	return path, nil
}

func TestLogEnvironment(t *testing.T) {
	t.Run("standalone mode", func(t *testing.T) {
		// Create a buffer to capture log output
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}))

		// Create systemd manager (should detect no systemd environment)
		systemdManager, err := NewSystemdManager()
		if err != nil {
			t.Fatalf("NewSystemdManager() error = %v", err)
		}
		defer systemdManager.Close()

		// Call the function under test
		LogEnvironment(context.Background(), logger, systemdManager, "/test/socket")

		// Check the log output contains expected messages
		logOutput := buf.String()
		expectedMessages := []string{"socket_path=/test/socket"}

		for _, expected := range expectedMessages {
			if !strings.Contains(logOutput, expected) {
				t.Errorf("expected log to contain %q, but got: %s", expected, logOutput)
			}
		}

		// Should contain either "running standalone" OR "running under systemd"
		if !strings.Contains(logOutput, "running standalone") && !strings.Contains(logOutput, "running under systemd") {
			t.Errorf("expected log to contain either 'running standalone' or 'running under systemd', but got: %s", logOutput)
		}
	})
}

func TestEnsureSocketDirErrorHandling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		socketPath string
		setup      func() string // Returns path to test, may modify filesystem
		cleanup    func(string)  // Cleanup after test
		wantErr    bool
	}{
		{
			name:       "success_case",
			socketPath: "/tmp/test-socket-dir/test.sock",
			setup:      func() string { return "/tmp/test-socket-dir/test.sock" },
			cleanup:    func(path string) { os.RemoveAll("/tmp/test-socket-dir") },
			wantErr:    false,
		},
		{
			name: "permission_denied",
			setup: func() string {
				// Try to create directory under a read-only parent
				// This may not work on all systems, so we'll use a more reliable approach
				if os.Getuid() == 0 { // Skip if running as root
					return ""
				}
				// Create a directory and make it read-only
				testDir := "/tmp/readonly-test-dir"
				os.Mkdir(testDir, 0755)
				os.Chmod(testDir, 0444) // Read-only
				return testDir + "/subdir/test.sock"
			},
			cleanup: func(path string) {
				if path != "" {
					os.Chmod("/tmp/readonly-test-dir", 0755) // Restore permissions
					os.RemoveAll("/tmp/readonly-test-dir")
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				testPath := tt.setup()
				if testPath == "" {
					t.Skip("test setup failed or not applicable")
				}
				if tt.cleanup != nil {
					defer tt.cleanup(testPath)
				}
				tt.socketPath = testPath
			}

			err := EnsureSocketDir(tt.socketPath)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestCleanupSocketErrorHandling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setup      func() string // Returns path to test
		cleanup    func(string)  // Cleanup after test
		wantErr    bool
	}{
		{
			name: "file_exists",
			setup: func() string {
				f, err := os.CreateTemp("", "test-socket-cleanup-*")
				if err != nil {
					return ""
				}
				path := f.Name()
				f.Close()
				return path
			},
			wantErr: false,
		},
		{
			name: "file_does_not_exist",
			setup: func() string {
				return "/tmp/non-existent-socket-file.sock"
			},
			wantErr: false, // Should not error for non-existent files
		},
		{
			name: "permission_denied",
			setup: func() string {
				if os.Getuid() == 0 { // Skip if running as root
					return ""
				}
				// Create a file in a directory, then make directory read-only
				dir := "/tmp/readonly-socket-dir"
				os.Mkdir(dir, 0755)

				f, err := os.Create(dir + "/test.sock")
				if err != nil {
					os.RemoveAll(dir)
					return ""
				}
				f.Close()

				os.Chmod(dir, 0444) // Make directory read-only
				return dir + "/test.sock"
			},
			cleanup: func(path string) {
				if path != "" {
					dir := "/tmp/readonly-socket-dir"
					os.Chmod(dir, 0755) // Restore permissions
					os.RemoveAll(dir)
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				testPath := tt.setup()
				if testPath == "" {
					t.Skip("test setup failed or not applicable")
				}
				if tt.cleanup != nil {
					defer tt.cleanup(testPath)
				}

				err := CleanupSocket(testPath)

				if tt.wantErr {
					if err == nil {
						t.Error("expected error but got nil")
					}
				} else {
					if err != nil {
						t.Errorf("unexpected error: %v", err)
					}
				}
			}
		})
	}
}
