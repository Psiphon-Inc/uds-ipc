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
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewWriter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		socketPath  string
		options     []WriterOption
		wantErr     bool
		expectedErr error
	}{
		{
			name:       "valid basic configuration",
			socketPath: "/tmp/test_writer.sock",
			options:    nil,
			wantErr:    false,
		},
		{
			name:       "valid with options",
			socketPath: "/tmp/test_writer.sock",
			options: []WriterOption{
				WithMaxBufferedWrites(500),
				WithWriteTimeout(2 * time.Second),
				WithDialTimeout(1 * time.Second),
				WithMaxBackoff(5 * time.Second),
			},
			wantErr: false,
		},
		{
			name:        "empty socket path",
			socketPath:  "",
			options:     nil,
			wantErr:     true,
			expectedErr: ErrInvalidSocketPath,
		},
		{
			name:       "zero write timeout",
			socketPath: "/tmp/test_writer.sock",
			options: []WriterOption{
				WithWriteTimeout(0),
			},
			wantErr: true,
		},
		{
			name:       "negative write timeout",
			socketPath: "/tmp/test_writer.sock",
			options: []WriterOption{
				WithWriteTimeout(-1 * time.Second),
			},
			wantErr: true,
		},
		{
			name:       "zero dial timeout",
			socketPath: "/tmp/test_writer.sock",
			options: []WriterOption{
				WithDialTimeout(0),
			},
			wantErr: true,
		},
		{
			name:       "zero max backoff",
			socketPath: "/tmp/test_writer.sock",
			options: []WriterOption{
				WithMaxBackoff(0),
			},
			wantErr: true,
		},
		{
			name:        "socket path too long",
			socketPath:  "/" + strings.Repeat("a", MaxSocketPathLength()+10),
			options:     nil,
			wantErr:     true,
			expectedErr: ErrInvalidSocketPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer, err := NewWriter(tt.socketPath, tt.options...)

			if tt.wantErr {
				if err == nil {
					t.Errorf("NewWriter() expected error, got nil")
					return
				}
				if tt.expectedErr != nil && !errors.Is(err, tt.expectedErr) {
					t.Errorf("NewWriter() error = %v, want %v", err, tt.expectedErr)
				}
				return
			}

			if err != nil {
				t.Errorf("NewWriter() unexpected error = %v", err)
				return
			}

			if writer == nil {
				t.Error("NewWriter() returned nil writer")
				return
			}

			// Verify socket path
			if writer.GetSocketPath() != tt.socketPath {
				t.Errorf("GetSocketPath() = %v, want %v", writer.GetSocketPath(), tt.socketPath)
			}

			// Clean up
			writer.Stop()
		})
	}
}

func TestWriterOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		option WriterOption
		verify func(*Writer) error
	}{
		{
			name:   "WithMaxBufferedWrites",
			option: WithMaxBufferedWrites(2048),
			verify: func(w *Writer) error {
				if cap(w.send) != 2048 {
					return fmt.Errorf("expected max buffered writes=2048, got %d", cap(w.send))
				}
				return nil
			},
		},
		{
			name:   "WithWriteTimeout",
			option: WithWriteTimeout(15 * time.Second),
			verify: func(w *Writer) error {
				if w.writeTimeout != 15*time.Second {
					return fmt.Errorf("expected writeTimeout=15s, got %v", w.writeTimeout)
				}
				return nil
			},
		},
		{
			name:   "WithDialTimeout",
			option: WithDialTimeout(3 * time.Second),
			verify: func(w *Writer) error {
				if w.dialTimeout != 3*time.Second {
					return fmt.Errorf("expected dialTimeout=3s, got %v", w.dialTimeout)
				}
				return nil
			},
		},
		{
			name:   "WithMaxBackoff",
			option: WithMaxBackoff(60 * time.Second),
			verify: func(w *Writer) error {
				if w.maxBackoff != 60*time.Second {
					return fmt.Errorf("expected maxBackoff=60s, got %v", w.maxBackoff)
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer, err := NewWriter("/tmp/test_writer.sock", tt.option)
			if err != nil {
				t.Fatalf("NewWriter() error = %v", err)
			}
			defer writer.Stop()

			if err := tt.verify(writer); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestWriterBasicOperation removed - redundant with TestReaderWriterIntegration
// which provides better end-to-end testing with actual Reader/Writer components

func TestWriterBufferFull(t *testing.T) {
	t.Parallel()
	socketPath, err := LocalPath()
	if err != nil {
		t.Fatalf("LocalPath() error = %v", err)
	}
	defer os.Remove(socketPath) // Clean up after test

	// Create writer with small buffer
	writer, err := NewWriter(socketPath, WithMaxBufferedWrites(2))
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	defer writer.Stop()

	writer.Start()

	// Fill buffer beyond capacity (no listener, so messages won't be consumed)
	for i := 0; i < 10; i++ {
		writer.WriteMessage([]byte(fmt.Sprintf("message %d", i)))
	}

	// Wait a bit for metrics to update
	time.Sleep(100 * time.Millisecond)

	sent, dropped, failed, queueDepth := writer.GetMetrics()

	if dropped == 0 {
		t.Error("expected some messages to be dropped")
	}

	if queueDepth != 2 {
		t.Errorf("expected queue depth = 2, got %d", queueDepth)
	}

	t.Logf("sent=%d, dropped=%d, failed=%d, queueDepth=%d", sent, dropped, failed, queueDepth)
}

func TestWriterConnectionFailure(t *testing.T) {
	t.Parallel()
	socketPath, err := LocalPath()
	if err != nil {
		t.Fatalf("LocalPath() error = %v", err)
	}
	defer os.Remove(socketPath) // Clean up after test

	var errorCallbacks []string
	var errorMu sync.Mutex

	errorCallback := func(err error, context string) {
		errorMu.Lock()
		errorCallbacks = append(errorCallbacks, context)
		errorMu.Unlock()
	}

	// Create writer with short timeouts
	writer, err := NewWriter(
		socketPath,
		WithWriterErrorCallback(errorCallback),
		WithDialTimeout(100*time.Millisecond),
		WithMaxBackoff(200*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	defer writer.Stop()

	writer.Start()

	// Send message (should fail to connect)
	writer.WriteMessage([]byte("test message"))

	// Wait for connection attempts and failures - need more time for retries
	time.Sleep(800 * time.Millisecond)

	// Check that error callback was called
	errorMu.Lock()
	hasConnectError := false
	for _, context := range errorCallbacks {
		if context == "failed to connect" {
			hasConnectError = true
			break
		}
	}
	errorMu.Unlock()

	if !hasConnectError {
		t.Error("expected 'failed to connect' error callback")
	}

	// Note: failedCount only tracks write failures after successful connections,
	// not connection failures. Connection failures are only reported via error callbacks.
}

func TestWriterReconnection(t *testing.T) {
	t.Parallel()
	socketPath, err := LocalPath()
	if err != nil {
		t.Fatalf("LocalPath() error = %v", err)
	}
	defer os.Remove(socketPath) // Clean up after test

	// Create writer
	writer, err := NewWriter(socketPath, WithMaxBackoff(100*time.Millisecond))
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	defer writer.Stop()

	// Add error callback to see what's happening
	writer, err = NewWriter(socketPath,
		WithMaxBackoff(100*time.Millisecond),
		WithWriterErrorCallback(func(err error, context string) {
			t.Logf("Writer error: %v (context: %s)", err, context)
		}),
	)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	defer writer.Stop()

	writer.Start()

	// Send message (will fail initially)
	writer.WriteMessage([]byte("test1"))
	time.Sleep(200 * time.Millisecond)

	// Start listener
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer listener.Close()

	var receivedMessages [][]byte
	var mu sync.Mutex

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		for {
			length, err := binary.ReadUvarint(reader)
			if err != nil {
				return
			}

			message := make([]byte, length)
			if _, err := reader.Read(message); err != nil {
				return
			}

			mu.Lock()
			receivedMessages = append(receivedMessages, message)
			mu.Unlock()
		}
	}()

	// Give time for listener to start
	time.Sleep(150 * time.Millisecond)

	// Send another message (should succeed after reconnection)
	writer.WriteMessage([]byte("test2"))

	// Wait longer for reconnection and message delivery
	time.Sleep(800 * time.Millisecond)

	// Check that at least one message was received
	mu.Lock()
	defer mu.Unlock()

	if len(receivedMessages) == 0 {
		t.Error("expected at least one message to be received after reconnection")
	}
}

func TestWriterClose(t *testing.T) {
	t.Parallel()
	socketPath, err := LocalPath()
	if err != nil {
		t.Fatalf("LocalPath() error = %v", err)
	}
	defer os.Remove(socketPath) // Clean up after test

	writer, err := NewWriter(socketPath)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}

	writer.Start()

	// Close should be idempotent
	err1 := writer.Stop()
	err2 := writer.Stop()

	if err1 != nil {
		t.Errorf("first Stop() error = %v", err1)
	}
	if err2 != nil {
		t.Errorf("second Stop() error = %v", err2)
	}
}

func TestWriterShutdownRaceConditions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		testFunc    func(*testing.T)
		description string
	}{
		{
			name:        "close_timing",
			description: "Stop() completes quickly without hanging",
			testFunc: func(t *testing.T) {
				socketPath, err := LocalPath()
				if err != nil {
					t.Fatalf("LocalPath() error = %v", err)
				}
				defer os.Remove(socketPath) // Clean up after test

				writer, err := NewWriter(socketPath)
				if err != nil {
					t.Fatalf("NewWriter() error = %v", err)
				}

				writer.Start()

				// Let writer attempt connection (may fail, that's ok)
				time.Sleep(10 * time.Millisecond)

				start := time.Now()
				err = writer.Stop()
				elapsed := time.Since(start)

				if elapsed > 500*time.Millisecond {
					t.Errorf("Writer.Stop() took too long: %v (expected < 500ms)", elapsed)
				}

				if err != nil {
					t.Errorf("Writer.Stop() error = %v", err)
				}
			},
		},
		{
			name:        "write_during_close",
			description: "WriteMessage() during Stop() doesn't cause races",
			testFunc: func(t *testing.T) {
				socketPath, err := LocalPath()
				if err != nil {
					t.Fatalf("LocalPath() error = %v", err)
				}
				defer os.Remove(socketPath) // Clean up after test

				writer, err := NewWriter(socketPath, WithMaxBufferedWrites(100))
				if err != nil {
					t.Fatalf("NewWriter() error = %v", err)
				}

				writer.Start()
				time.Sleep(10 * time.Millisecond)

				// Start writing messages concurrently with close
				done := make(chan struct{})
				go func() {
					defer close(done)
					for i := 0; i < 50; i++ {
						writer.WriteMessage([]byte("test message"))
						if i%10 == 0 {
							time.Sleep(time.Microsecond) // Small delay to increase race probability
						}
					}
				}()

				// Close while writes are happening
				time.Sleep(5 * time.Millisecond)
				start := time.Now()
				err = writer.Stop()
				elapsed := time.Since(start)

				<-done // Wait for writes to finish

				if elapsed > 1*time.Second {
					t.Errorf("Writer.Stop() during writes took too long: %v", elapsed)
				}

				if err != nil {
					t.Errorf("Writer.Stop() error = %v", err)
				}

				// Verify metrics are consistent (no corruption from race)
				sent, dropped, failed, _ := writer.GetMetrics()
				total := sent + dropped + failed
				if total > 50 {
					t.Errorf("Metric corruption detected: sent=%d + dropped=%d + failed=%d = %d > 50",
						sent, dropped, failed, total)
				}
			},
		},
		{
			name:        "concurrent_close",
			description: "Multiple concurrent Stop() calls don't race",
			testFunc: func(t *testing.T) {
				socketPath, err := LocalPath()
				if err != nil {
					t.Fatalf("LocalPath() error = %v", err)
				}
				defer os.Remove(socketPath) // Clean up after test

				writer, err := NewWriter(socketPath)
				if err != nil {
					t.Fatalf("NewWriter() error = %v", err)
				}

				writer.Start()
				time.Sleep(10 * time.Millisecond)

				// Start multiple concurrent Stop() calls
				const numGoroutines = 10
				var wg sync.WaitGroup
				errors := make(chan error, numGoroutines)

				start := time.Now()

				for i := 0; i < numGoroutines; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						errors <- writer.Stop()
					}()
				}

				wg.Wait()
				elapsed := time.Since(start)

				if elapsed > 1*time.Second {
					t.Errorf("Concurrent Stop() took too long: %v", elapsed)
				}

				// Check that all Stop() calls succeeded
				close(errors)
				for err := range errors {
					if err != nil {
						t.Errorf("Stop() error in concurrent test: %v", err)
					}
				}
			},
		},
		{
			name:        "close_during_connection",
			description: "Stop() during connection attempts doesn't hang",
			testFunc: func(t *testing.T) {
				// Use a non-existent path to force connection failures
				socketPath := "/tmp/nonexistent/test.sock"

				writer, err := NewWriter(socketPath)
				if err != nil {
					t.Fatalf("NewWriter() error = %v", err)
				}

				writer.Start()

				// Let writer get stuck in connection retry loop
				time.Sleep(50 * time.Millisecond)

				start := time.Now()
				err = writer.Stop()
				elapsed := time.Since(start)

				if elapsed > 1*time.Second {
					t.Errorf("Writer.Stop() during connection took too long: %v (expected < 1s)", elapsed)
				}

				if err != nil {
					t.Errorf("Writer.Stop() error = %v", err)
				}
			},
		},
		{
			name:        "close_after_write_failure",
			description: "Stop() after write failures doesn't hang",
			testFunc: func(t *testing.T) {
				socketPath, err := LocalPath()
				if err != nil {
					t.Fatalf("LocalPath() error = %v", err)
				}
				defer os.Remove(socketPath) // Clean up after test

				// Create a mock server that accepts but immediately closes connections
				listener, err := net.Listen("unix", socketPath)
				if err != nil {
					t.Fatalf("net.Listen() error = %v", err)
				}
				defer listener.Close()

				go func() {
					for {
						conn, err := listener.Accept()
						if err != nil {
							return
						}
						conn.Close() // Immediately close to cause write failures
					}
				}()

				writer, err := NewWriter(socketPath)
				if err != nil {
					t.Fatalf("NewWriter() error = %v", err)
				}

				writer.Start()

				// Send messages that will fail due to closed connections
				for i := 0; i < 10; i++ {
					writer.WriteMessage([]byte("test"))
					time.Sleep(5 * time.Millisecond)
				}

				start := time.Now()
				err = writer.Stop()
				elapsed := time.Since(start)

				if elapsed > 1*time.Second {
					t.Errorf("Writer.Stop() after failures took too long: %v", elapsed)
				}

				if err != nil {
					t.Errorf("Writer.Stop() error = %v", err)
				}

				// Verify some failures were recorded
				_, _, failed, _ := writer.GetMetrics()
				if failed == 0 {
					t.Error("Expected some write failures to be recorded")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			t.Logf("Testing: %s", tt.description)

			// Wrap test in timeout to catch hangs
			done := make(chan struct{})
			go func() {
				defer close(done)
				tt.testFunc(t)
			}()

			select {
			case <-done:
				// Test completed successfully
			case <-time.After(10 * time.Second):
				t.Fatal("Test hung - potential race condition or deadlock detected")
			}
		})
	}
}

func TestWriterWriteFailure(t *testing.T) {
	t.Parallel()
	socketPath, err := LocalPath()
	if err != nil {
		t.Fatalf("LocalPath() error = %v", err)
	}
	defer os.Remove(socketPath) // Clean up after test

	// Set up a listener that accepts but immediately closes connections
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close() // Immediately close to simulate write failure
		}
	}()

	var writeFailures int32
	errorCallback := func(err error, context string) {
		if context == "write failure" {
			atomic.AddInt32(&writeFailures, 1)
		}
	}

	writer, err := NewWriter(
		socketPath,
		WithWriterErrorCallback(errorCallback),
		WithWriteTimeout(100*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	defer writer.Stop()

	writer.Start()

	// Send messages that should fail to write
	for i := 0; i < 3; i++ {
		writer.WriteMessage([]byte(fmt.Sprintf("message %d", i)))
		time.Sleep(150 * time.Millisecond) // Allow time for connection and write failure
	}

	// Check that write failures were detected
	if atomic.LoadInt32(&writeFailures) == 0 {
		t.Error("expected write failure callbacks")
	}

	// Check metrics
	_, _, failed, _ := writer.GetMetrics()
	if failed == 0 {
		t.Error("expected failed count > 0")
	}
}

func TestWriterRetryOnFailure(t *testing.T) {
	t.Parallel()

	// Simple test to understand the exact call pattern
	t.Run("debug_call_pattern", func(t *testing.T) {
		writer := &Writer{}
		mockConn := &mockConnection{
			writeCallCount: 0,
			failOnCalls:    make(map[int]bool),
		}
		writer.conn = mockConn

		data := []byte("test message")
		err := writer.writeLengthPrefixedData(data)

		t.Logf("No failures: calls=%d, error=%v", mockConn.writeCallCount, err)
	})

	t.Run("debug_first_call_fails", func(t *testing.T) {
		writer := &Writer{}
		mockConn := &mockConnection{
			writeCallCount: 0,
			failOnCalls:    map[int]bool{1: true},
		}
		writer.conn = mockConn

		data := []byte("test message")
		err := writer.writeLengthPrefixedData(data)

		t.Logf("First call fails: calls=%d, error=%v", mockConn.writeCallCount, err)
	})

	t.Run("debug_second_call_fails", func(t *testing.T) {
		writer := &Writer{}
		mockConn := &mockConnection{
			writeCallCount: 0,
			failOnCalls:    map[int]bool{2: true},
		}
		writer.conn = mockConn

		data := []byte("test message")
		err := writer.writeLengthPrefixedData(data)

		t.Logf("Second call fails: calls=%d, error=%v", mockConn.writeCallCount, err)
	})
}

// mockConnection simulates connection behavior for testing retry logic
type mockConnection struct {
	writeCallCount int
	failOnCalls    map[int]bool // Which call numbers should fail (1-indexed)
}

func (m *mockConnection) Write(b []byte) (int, error) {
	m.writeCallCount++

	if m.failOnCalls[m.writeCallCount] {
		return 0, fmt.Errorf("simulated write failure on call %d", m.writeCallCount)
	}

	// Simulate successful write
	return len(b), nil
}

func (m *mockConnection) SetWriteDeadline(t time.Time) error {
	return nil // Mock always succeeds
}

func (m *mockConnection) Close() error                    { return nil }
func (m *mockConnection) Read([]byte) (int, error)        { return 0, nil }
func (m *mockConnection) LocalAddr() net.Addr             { return nil }
func (m *mockConnection) RemoteAddr() net.Addr            { return nil }
func (m *mockConnection) SetDeadline(time.Time) error     { return nil }
func (m *mockConnection) SetReadDeadline(time.Time) error { return nil }

func TestWriterEmptyMessageFiltering(t *testing.T) {
	t.Parallel()
	socketPath, err := LocalPath()
	if err != nil {
		t.Fatalf("LocalPath() error = %v", err)
	}
	defer os.Remove(socketPath) // Clean up after test

	writer, err := NewWriter(socketPath)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	defer writer.Stop()

	writer.Start()

	// Send a mix of empty and non-empty messages
	writer.WriteMessage([]byte(""))        // Should be filtered
	writer.WriteMessage([]byte("message")) // Should be queued
	writer.WriteMessage([]byte{})          // Should be filtered
	writer.WriteMessage([]byte("test"))    // Should be queued

	// Wait a moment for messages to be processed
	time.Sleep(50 * time.Millisecond)

	// Check metrics - only non-empty messages should be in the queue
	sent, dropped, failed, queueDepth := writer.GetMetrics()

	// Since there's no listener, messages will remain queued (not sent)
	// We expect 2 messages in the queue (the non-empty ones)
	if queueDepth != 2 {
		t.Errorf("expected queue depth = 2 (empty messages filtered), got %d", queueDepth)
	}

	// Sent count should be 0 since no listener is connected
	if sent != 0 {
		t.Errorf("expected sent = 0 (no listener), got %d", sent)
	}

	// Dropped count should be 0 since buffer isn't full
	if dropped != 0 {
		t.Errorf("expected dropped = 0 (buffer not full), got %d", dropped)
	}

	t.Logf("Empty message filtering: sent=%d, dropped=%d, failed=%d, queueDepth=%d", sent, dropped, failed, queueDepth)
}
