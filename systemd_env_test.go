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
	"os"
	"path/filepath"
	"testing"
)

func TestSystemdEnvironmentVariables(t *testing.T) {
	// Note: Cannot use t.Parallel() because we're modifying global environment variables

	// Save original environment
	originalRuntime := os.Getenv("RUNTIME_DIRECTORY")
	originalState := os.Getenv("STATE_DIRECTORY")
	originalListenFds := os.Getenv("LISTEN_FDS")
	originalNotifySocket := os.Getenv("NOTIFY_SOCKET")

	defer func() {
		// Restore original environment
		if originalRuntime != "" {
			os.Setenv("RUNTIME_DIRECTORY", originalRuntime)
		} else {
			os.Unsetenv("RUNTIME_DIRECTORY")
		}
		if originalState != "" {
			os.Setenv("STATE_DIRECTORY", originalState)
		} else {
			os.Unsetenv("STATE_DIRECTORY")
		}
		if originalListenFds != "" {
			os.Setenv("LISTEN_FDS", originalListenFds)
		} else {
			os.Unsetenv("LISTEN_FDS")
		}
		if originalNotifySocket != "" {
			os.Setenv("NOTIFY_SOCKET", originalNotifySocket)
		} else {
			os.Unsetenv("NOTIFY_SOCKET")
		}
	}()

	tests := []struct {
		name             string
		runtimeDir       string
		stateDir         string
		expectSystemd    bool
		expectRuntimeDir string
		expectStateDir   string
	}{
		{
			name:             "no systemd environment",
			runtimeDir:       "",
			stateDir:         "",
			expectSystemd:    false,
			expectRuntimeDir: "",
			expectStateDir:   "",
		},
		{
			name:             "runtime directory only",
			runtimeDir:       "/run/myservice",
			stateDir:         "",
			expectSystemd:    true,
			expectRuntimeDir: "/run/myservice",
			expectStateDir:   "",
		},
		{
			name:             "state directory only",
			runtimeDir:       "",
			stateDir:         "/var/lib/myservice",
			expectSystemd:    false, // STATE_DIRECTORY alone doesn't trigger systemd mode
			expectRuntimeDir: "",
			expectStateDir:   "/var/lib/myservice",
		},
		{
			name:             "both directories",
			runtimeDir:       "/run/myservice",
			stateDir:         "/var/lib/myservice",
			expectSystemd:    true,
			expectRuntimeDir: "/run/myservice",
			expectStateDir:   "/var/lib/myservice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all systemd environment variables first
			os.Unsetenv("RUNTIME_DIRECTORY")
			os.Unsetenv("STATE_DIRECTORY")
			os.Unsetenv("LISTEN_FDS")
			os.Unsetenv("NOTIFY_SOCKET")

			// Set test-specific environment
			if tt.runtimeDir != "" {
				os.Setenv("RUNTIME_DIRECTORY", tt.runtimeDir)
			}
			if tt.stateDir != "" {
				os.Setenv("STATE_DIRECTORY", tt.stateDir)
			}

			manager, err := NewSystemdManager()
			if err != nil {
				t.Fatalf("NewSystemdManager() error = %v", err)
			}

			if manager.IsSystemd() != tt.expectSystemd {
				t.Errorf("IsSystemd() = %v, want %v", manager.IsSystemd(), tt.expectSystemd)
			}

			if manager.GetRuntimeDir() != tt.expectRuntimeDir {
				t.Errorf("GetRuntimeDir() = %q, want %q", manager.GetRuntimeDir(), tt.expectRuntimeDir)
			}

			if manager.GetStateDir() != tt.expectStateDir {
				t.Errorf("GetStateDir() = %q, want %q", manager.GetStateDir(), tt.expectStateDir)
			}
		})
	}
}

func TestResolveSocketPathWithSystemdDirs(t *testing.T) {
	// Note: Cannot use t.Parallel() because we're modifying global environment variables

	// Save original environment
	originalRuntime := os.Getenv("RUNTIME_DIRECTORY")
	originalState := os.Getenv("STATE_DIRECTORY")
	originalListenFds := os.Getenv("LISTEN_FDS")
	originalNotifySocket := os.Getenv("NOTIFY_SOCKET")

	defer func() {
		// Restore original environment
		if originalRuntime != "" {
			os.Setenv("RUNTIME_DIRECTORY", originalRuntime)
		} else {
			os.Unsetenv("RUNTIME_DIRECTORY")
		}
		if originalState != "" {
			os.Setenv("STATE_DIRECTORY", originalState)
		} else {
			os.Unsetenv("STATE_DIRECTORY")
		}
		if originalListenFds != "" {
			os.Setenv("LISTEN_FDS", originalListenFds)
		} else {
			os.Unsetenv("LISTEN_FDS")
		}
		if originalNotifySocket != "" {
			os.Setenv("NOTIFY_SOCKET", originalNotifySocket)
		} else {
			os.Unsetenv("NOTIFY_SOCKET")
		}
	}()

	tempDir := t.TempDir()
	runtimeDir := filepath.Join(tempDir, "runtime")
	fallbackPath := "/tmp/fallback.sock"

	tests := []struct {
		name         string
		setupSystemd bool
		runtimeDir   string
		expectedPath string
	}{
		{
			name:         "no systemd - use fallback",
			setupSystemd: false,
			runtimeDir:   "",
			expectedPath: fallbackPath,
		},
		{
			name:         "systemd with runtime dir",
			setupSystemd: true,
			runtimeDir:   runtimeDir,
			expectedPath: filepath.Join(runtimeDir, "fallback.sock"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all systemd environment variables first
			os.Unsetenv("RUNTIME_DIRECTORY")
			os.Unsetenv("STATE_DIRECTORY")
			os.Unsetenv("LISTEN_FDS")
			os.Unsetenv("NOTIFY_SOCKET")

			if tt.setupSystemd {
				os.Setenv("RUNTIME_DIRECTORY", tt.runtimeDir)
			}

			manager, err := NewSystemdManager()
			if err != nil {
				t.Fatalf("NewSystemdManager() error = %v", err)
			}

			actualPath := ResolveSocketPath(manager, fallbackPath)
			if actualPath != tt.expectedPath {
				t.Errorf("ResolveSocketPath() = %q, want %q", actualPath, tt.expectedPath)
			}
		})
	}
}
