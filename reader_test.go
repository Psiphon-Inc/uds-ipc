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
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewReader(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name               string
		handler            MessageHandler
		fallbackSocketPath string
		options            []ReaderOption
		wantErr            bool
		expectedErr        error
	}{
		{
			name:               "valid basic configuration",
			handler:            func([]byte) error { return nil },
			fallbackSocketPath: "/tmp/test.sock",
			options:            nil,
			wantErr:            false,
		},
		{
			name:               "valid with options",
			handler:            func([]byte) error { return nil },
			fallbackSocketPath: "/tmp/test.sock",
			options: []ReaderOption{
				WithMaxMessageSize(1024),
				WithInactivityTimeout(5 * time.Second),
				WithMaxAcceptErrors(5),
			},
			wantErr: false,
		},
		{
			name:               "nil handler",
			handler:            nil,
			fallbackSocketPath: "/tmp/test.sock",
			options:            nil,
			wantErr:            true,
			expectedErr:        ErrHandlerNil,
		},
		{
			name:               "empty socket path",
			handler:            func([]byte) error { return nil },
			fallbackSocketPath: "",
			options:            nil,
			wantErr:            true,
			expectedErr:        ErrInvalidSocketPath,
		},
		{
			name:               "invalid max accept errors",
			handler:            func([]byte) error { return nil },
			fallbackSocketPath: "/tmp/test.sock",
			options: []ReaderOption{
				WithMaxAcceptErrors(64), // > 63 should fail
			},
			wantErr:     true,
			expectedErr: ErrMaxAcceptErrorsTooLarge,
		},
		{
			name:               "invalid inactivity timeout",
			handler:            func([]byte) error { return nil },
			fallbackSocketPath: "/tmp/test.sock",
			options: []ReaderOption{
				WithInactivityTimeout(0),
			},
			wantErr: true,
		},
		{
			name:               "socket path too long",
			handler:            func([]byte) error { return nil },
			fallbackSocketPath: "/" + strings.Repeat("a", MaxSocketPathLength()+10),
			options:            nil,
			wantErr:            true,
			expectedErr:        ErrInvalidSocketPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use unique socket path for each test to avoid conflicts
			var socketPath string
			if tt.fallbackSocketPath == "" {
				socketPath = ""
			} else if len(tt.fallbackSocketPath) > MaxSocketPathLength() {
				// For socket path length tests, use the exact path provided
				socketPath = tt.fallbackSocketPath
			} else {
				var err error
				socketPath, err = LocalPath()
				if err != nil {
					t.Fatalf("LocalPath() error = %v", err)
				}
				defer os.Remove(socketPath) // Clean up after test
			}

			reader, err := NewReader(tt.handler, socketPath, tt.options...)

			if tt.wantErr {
				if err == nil {
					t.Errorf("NewReader() expected error, got nil")
					return
				}
				if tt.expectedErr != nil && !errors.Is(err, tt.expectedErr) {
					t.Errorf("NewReader() error = %v, want %v", err, tt.expectedErr)
				}
				return
			}

			if err != nil {
				t.Errorf("NewReader() unexpected error = %v", err)
				return
			}

			if reader == nil {
				t.Error("NewReader() returned nil reader")
				return
			}

			// Clean up
			if reader.listener != nil {
				reader.Stop()
			}
		})
	}
}

func TestReaderOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		option ReaderOption
		verify func(*Reader) error
	}{
		{
			name:   "WithMaxMessageSize",
			option: WithMaxMessageSize(2048),
			verify: func(r *Reader) error {
				if r.maxMessageSize != 2048 {
					return fmt.Errorf("expected maxMessageSize=2048, got %d", r.maxMessageSize)
				}
				return nil
			},
		},
		{
			name:   "WithInactivityTimeout",
			option: WithInactivityTimeout(15 * time.Second),
			verify: func(r *Reader) error {
				if r.inactivityTimeout != 15*time.Second {
					return fmt.Errorf("expected inactivityTimeout=15s, got %v", r.inactivityTimeout)
				}
				return nil
			},
		},
		{
			name:   "WithMaxAcceptErrors",
			option: WithMaxAcceptErrors(20),
			verify: func(r *Reader) error {
				if r.maxAcceptErrors != 20 {
					return fmt.Errorf("expected maxAcceptErrors=20, got %d", r.maxAcceptErrors)
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader, err := NewReader(
				func([]byte) error { return nil },
				"/tmp/test.sock",
				tt.option,
			)
			if err != nil {
				t.Fatalf("NewReader() error = %v", err)
			}
			defer reader.Stop()

			if err := tt.verify(reader); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestReaderMessageHandling(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		messages       [][]byte
		maxMessageSize uint64
		handler        MessageHandler
		expectError    bool
		expectFiltered bool
		errorCallback  func(*testing.T) ErrorCallback
	}{
		{
			name:     "single message",
			messages: [][]byte{[]byte("hello")},
			handler:  func(data []byte) error { return nil },
		},
		{
			name:     "multiple messages",
			messages: [][]byte{[]byte("msg1"), []byte("msg2"), []byte("msg3")},
			handler:  func(data []byte) error { return nil },
		},
		{
			name:           "empty message",
			messages:       [][]byte{[]byte("")},
			handler:        func(data []byte) error { return nil },
			expectFiltered: true, // Empty messages are filtered out
		},
		{
			name:           "message too large",
			messages:       [][]byte{make([]byte, 1001)}, // > maxMessageSize
			maxMessageSize: 1000,
			handler:        func(data []byte) error { return nil },
			expectError:    true,
			errorCallback: func(t *testing.T) ErrorCallback {
				return func(err error, context string) {
					if context != "message too large" {
						t.Errorf("expected context 'message too large', got '%s'", context)
					}
				}
			},
		},
		{
			name:        "handler error",
			messages:    [][]byte{[]byte("test")},
			handler:     func(data []byte) error { return errors.New("handler failed") },
			expectError: true,
			errorCallback: func(t *testing.T) ErrorCallback {
				return func(err error, context string) {
					if context != "handler failed to process a message" {
						t.Errorf("expected context 'handler failed to process a message', got '%s'", context)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			socketPath, err := LocalPath()
			if err != nil {
				t.Fatalf("LocalPath() error = %v", err)
			}
			defer os.Remove(socketPath) // Clean up after test

			var receivedMessages [][]byte
			var mu sync.Mutex

			options := []ReaderOption{}
			if tt.maxMessageSize > 0 {
				options = append(options, WithMaxMessageSize(tt.maxMessageSize))
			}
			if tt.errorCallback != nil {
				options = append(options, WithReaderErrorCallback(tt.errorCallback(t)))
			}

			// Wrap handler to collect messages
			handler := func(data []byte) error {
				mu.Lock()
				receivedMessages = append(receivedMessages, append([]byte(nil), data...))
				mu.Unlock()
				return tt.handler(data)
			}

			reader, err := NewReader(handler, socketPath, options...)
			if err != nil {
				t.Fatalf("NewReader() error = %v", err)
			}
			defer reader.Stop()

			if err := reader.Start(); err != nil {
				t.Fatalf("reader.Start() error = %v", err)
			}

			// Give reader time to start listening
			time.Sleep(100 * time.Millisecond)

			// Send messages
			for _, msg := range tt.messages {
				conn, err := net.Dial("unix", socketPath)
				if err != nil {
					t.Fatalf("net.Dial() error = %v", err)
				}

				// Write length prefix
				lengthBuf := make([]byte, binary.MaxVarintLen64)
				n := binary.PutUvarint(lengthBuf, uint64(len(msg)))
				if _, err := conn.Write(lengthBuf[:n]); err != nil {
					conn.Close()
					t.Fatalf("Write length prefix error = %v", err)
				}

				// Write message
				if _, err := conn.Write(msg); err != nil {
					conn.Close()
					t.Fatalf("Write message error = %v", err)
				}

				conn.Close()
			}

			// Wait for processing
			time.Sleep(200 * time.Millisecond)

			// Verify results
			if !tt.expectError && !tt.expectFiltered {
				mu.Lock()
				if len(receivedMessages) != len(tt.messages) {
					t.Errorf("expected %d messages, got %d", len(tt.messages), len(receivedMessages))
				}
				// For multiple messages, just verify all expected messages were received (order may vary due to concurrent connections)
				if len(tt.messages) > 1 {
					expectedSet := make(map[string]bool)
					for _, msg := range tt.messages {
						expectedSet[string(msg)] = true
					}
					for _, received := range receivedMessages {
						if !expectedSet[string(received)] {
							t.Errorf("unexpected message received: %q", received)
						}
					}
				} else {
					// For single message, check exact match
					for i, expected := range tt.messages {
						if i < len(receivedMessages) && string(receivedMessages[i]) != string(expected) {
							t.Errorf("message %d: expected %q, got %q", i, expected, receivedMessages[i])
						}
					}
				}
				mu.Unlock()
			}

			// Check metrics
			received, connections, errorCount := reader.GetMetrics()
			if !tt.expectError && !tt.expectFiltered && received == 0 {
				t.Error("expected received count > 0")
			}
			if tt.expectFiltered && received != 0 {
				t.Errorf("expected filtered messages (received=0), got received=%d", received)
			}
			if connections == 0 {
				t.Error("expected connection count > 0")
			}
			if tt.expectError && errorCount == 0 {
				t.Error("expected error count > 0")
			}
		})
	}
}

func TestReaderClose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		testIdempotent bool
		testTiming     bool
		maxDuration    time.Duration
	}{
		{
			name:           "idempotent close",
			testIdempotent: true,
			testTiming:     false,
		},
		{
			name:           "no hang on close",
			testIdempotent: false,
			testTiming:     true,
			maxDuration:    1 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			socketPath, err := LocalPath()
			if err != nil {
				t.Fatalf("LocalPath() error = %v", err)
			}
			defer os.Remove(socketPath) // Clean up after test

			reader, err := NewReader(
				func([]byte) error { return nil },
				socketPath,
			)
			if err != nil {
				t.Fatalf("NewReader() error = %v", err)
			}

			if err := reader.Start(); err != nil {
				t.Fatalf("reader.Start() error = %v", err)
			}

			// Let the reader settle into its Accept() loop for timing tests
			if tt.testTiming {
				time.Sleep(50 * time.Millisecond)
			}

			if tt.testIdempotent {
				// Test that Stop() is idempotent
				err1 := reader.Stop()
				err2 := reader.Stop()

				if err1 != nil {
					t.Errorf("first Stop() error = %v", err1)
				}
				if err2 != nil {
					t.Errorf("second Stop() error = %v", err2)
				}
			}

			if tt.testTiming {
				// Test that Stop() completes quickly (prevents regression of hanging issue)
				start := time.Now()

				done := make(chan error, 1)
				go func() {
					done <- reader.Stop()
				}()

				select {
				case err := <-done:
					elapsed := time.Since(start)

					if elapsed > tt.maxDuration {
						t.Errorf("Reader.Stop() took too long: %v (expected < %v)", elapsed, tt.maxDuration)
					}

					if err != nil {
						t.Errorf("Reader.Stop() error = %v", err)
					}

				case <-time.After(2 * time.Second):
					t.Fatal("Reader.Stop() hung - shutdown mechanism failed")
				}
			}
		})
	}
}

func TestReaderContextCancellation(t *testing.T) {
	t.Parallel()
	socketPath, err := LocalPath()
	if err != nil {
		t.Fatalf("LocalPath() error = %v", err)
	}
	defer os.Remove(socketPath) // Clean up after test

	reader, err := NewReader(
		func([]byte) error { return nil },
		socketPath,
	)
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	defer reader.Stop()

	if err := reader.Start(); err != nil {
		t.Fatalf("reader.Start() error = %v", err)
	}

	// Give reader time to start
	time.Sleep(100 * time.Millisecond)

	// Should be able to stop cleanly
	if err := reader.Stop(); err != nil {
		t.Errorf("Stop() error = %v", err)
	}
}
