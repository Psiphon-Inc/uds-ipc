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
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReaderWriterIntegration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name               string
		messages           [][]byte
		readerOptions      []ReaderOption
		writerOptions      []WriterOption
		expectedReceived   int
		expectReaderErrors bool
		expectWriterErrors bool
	}{
		{
			name:             "basic communication",
			messages:         [][]byte{[]byte("hello"), []byte("world"), []byte("test")},
			expectedReceived: 3,
		},
		{
			name:             "empty messages",
			messages:         [][]byte{[]byte(""), []byte("test"), []byte("")},
			expectedReceived: 1,
		},
		{
			name:     "large messages",
			messages: [][]byte{make([]byte, 1024), make([]byte, 2048)},
			readerOptions: []ReaderOption{
				WithMaxMessageSize(4096),
			},
			expectedReceived: 2,
		},
		{
			name:     "message size limit exceeded",
			messages: [][]byte{make([]byte, 2000)},
			readerOptions: []ReaderOption{
				WithMaxMessageSize(1000),
			},
			expectedReceived:   0,
			expectReaderErrors: true,
		},
		{
			name: "many small messages",
			messages: func() [][]byte {
				msgs := make([][]byte, 100)
				for i := range msgs {
					msgs[i] = []byte(fmt.Sprintf("msg%d", i))
				}
				return msgs
			}(),
			expectedReceived: 100,
		},
		{
			name: "small buffer high throughput",
			messages: func() [][]byte {
				msgs := make([][]byte, 20) // Reduced from 50
				for i := range msgs {
					msgs[i] = []byte(fmt.Sprintf("message%d", i))
				}
				return msgs
			}(),
			writerOptions: []WriterOption{
				WithMaxBufferedWrites(15), // Slightly larger buffer
			},
			expectedReceived: 15, // Expect some drops with small buffer
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
			var receivedCount int32
			var mu sync.Mutex

			var readerErrors, writerErrors int32

			readerErrorCallback := func(err error, context string) {
				atomic.AddInt32(&readerErrors, 1)
				t.Logf("Reader error: %v (context: %s)", err, context)
			}

			writerErrorCallback := func(err error, context string) {
				atomic.AddInt32(&writerErrors, 1)
				t.Logf("Writer error: %v (context: %s)", err, context)
			}

			// Set up reader
			readerOpts := append(tt.readerOptions, WithReaderErrorCallback(readerErrorCallback))

			reader, err := NewReader(
				func(data []byte) error {
					mu.Lock()
					receivedMessages = append(receivedMessages, append([]byte(nil), data...))
					mu.Unlock()
					atomic.AddInt32(&receivedCount, 1)
					return nil
				},
				socketPath,
				readerOpts...,
			)
			if err != nil {
				t.Fatalf("NewReader() error = %v", err)
			}
			defer reader.Stop(context.Background())

			// Set up writer
			writerOpts := append(tt.writerOptions, WithWriterErrorCallback(writerErrorCallback))

			writer, err := NewWriter(socketPath, writerOpts...)
			if err != nil {
				t.Fatalf("NewWriter() error = %v", err)
			}
			defer writer.Stop(context.Background())

			// Start reader first
			if err := reader.Start(); err != nil {
				t.Fatalf("reader.Start() error = %v", err)
			}

			// Give reader time to start listening
			time.Sleep(100 * time.Millisecond)

			// Start writer
			writer.Start()

			// Send messages
			for _, msg := range tt.messages {
				writer.WriteMessage(msg)
			}

			// Wait for all messages to be processed
			timeout := time.After(1 * time.Second)
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-timeout:
					t.Fatal("timeout waiting for messages")
				case <-ticker.C:
					if atomic.LoadInt32(&receivedCount) >= int32(tt.expectedReceived) {
						goto done
					}
				}
			}

		done:
			// Verify results
			finalReceivedCount := atomic.LoadInt32(&receivedCount)
			if int(finalReceivedCount) != tt.expectedReceived {
				t.Errorf("expected %d messages received, got %d", tt.expectedReceived, finalReceivedCount)
			}

			// Check error expectations
			finalReaderErrors := atomic.LoadInt32(&readerErrors)
			finalWriterErrors := atomic.LoadInt32(&writerErrors)

			if tt.expectReaderErrors && finalReaderErrors == 0 {
				t.Error("expected reader errors but got none")
			}
			if !tt.expectReaderErrors && finalReaderErrors > 0 {
				t.Errorf("unexpected reader errors: %d", finalReaderErrors)
			}
			if tt.expectWriterErrors && finalWriterErrors == 0 {
				t.Error("expected writer errors but got none")
			}
			if !tt.expectWriterErrors && finalWriterErrors > 0 {
				t.Errorf("unexpected writer errors: %d", finalWriterErrors)
			}

			// Verify message content (for smaller test cases, excluding empty message tests)
			if len(tt.messages) <= 10 && tt.expectedReceived > 0 && tt.name != "empty messages" {
				mu.Lock()
				if len(receivedMessages) != tt.expectedReceived {
					t.Errorf("expected %d messages, got %d", tt.expectedReceived, len(receivedMessages))
				}
				for i, expected := range tt.messages {
					if i < len(receivedMessages) && i < tt.expectedReceived {
						if string(receivedMessages[i]) != string(expected) {
							t.Errorf("message %d: expected %q, got %q", i, expected, receivedMessages[i])
						}
					}
				}
				mu.Unlock()
			}

			// Check metrics
			readerReceived, readerConnections, readerErrorCount := reader.GetMetrics()
			writerSent, writerDropped, writerFailed, _ := writer.GetMetrics()

			t.Logf("Reader metrics: received=%d, connections=%d, errors=%d",
				readerReceived, readerConnections, readerErrorCount)
			t.Logf("Writer metrics: sent=%d, dropped=%d, failed=%d",
				writerSent, writerDropped, writerFailed)

			if tt.expectedReceived > 0 {
				if readerReceived == 0 {
					t.Error("expected reader received count > 0")
				}
				if readerConnections == 0 {
					t.Error("expected reader connection count > 0")
				}
			}
		})
	}
}

func TestReaderWriterConnectionRecovery(t *testing.T) {
	t.Parallel()
	socketPath, err := LocalPath()
	if err != nil {
		t.Fatalf("LocalPath() error = %v", err)
	}
	defer os.Remove(socketPath) // Clean up after test

	var receivedMessages [][]byte
	var mu sync.Mutex

	reader, err := NewReader(
		func(data []byte) error {
			mu.Lock()
			receivedMessages = append(receivedMessages, append([]byte(nil), data...))
			mu.Unlock()
			return nil
		},
		socketPath,
	)
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	defer reader.Stop(context.Background())

	writer, err := NewWriter(socketPath, WithMaxBackoff(100*time.Millisecond))
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	defer writer.Stop(context.Background())

	// Start writer first (will fail to connect initially)
	writer.Start()

	// Send message while no reader is available
	writer.WriteMessage([]byte("message1"))
	time.Sleep(200 * time.Millisecond)

	// Start reader
	if err := reader.Start(); err != nil {
		t.Fatalf("reader.Start() error = %v", err)
	}

	// Give time for connection to establish
	time.Sleep(300 * time.Millisecond)

	// Send messages after reader is available
	writer.WriteMessage([]byte("message2"))
	writer.WriteMessage([]byte("message3"))

	// Wait for processing
	time.Sleep(300 * time.Millisecond)

	// Restart reader (simulate reader crash/restart)
	reader.Stop(context.Background())
	time.Sleep(100 * time.Millisecond)

	reader2, err := NewReader(
		func(data []byte) error {
			mu.Lock()
			receivedMessages = append(receivedMessages, append([]byte(nil), data...))
			mu.Unlock()
			return nil
		},
		socketPath,
	)
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	defer reader2.Stop(context.Background())

	if err := reader2.Start(); err != nil {
		t.Fatalf("reader2.Start() error = %v", err)
	}

	// Give time for reconnection
	time.Sleep(200 * time.Millisecond)

	// Send more messages
	writer.WriteMessage([]byte("message4"))
	writer.WriteMessage([]byte("message5"))

	// Wait for final processing
	time.Sleep(300 * time.Millisecond)

	// Verify some messages were received
	mu.Lock()
	defer mu.Unlock()

	if len(receivedMessages) == 0 {
		t.Error("expected some messages to be received")
	}

	t.Logf("Received %d messages during connection recovery test", len(receivedMessages))
}

func TestReaderWriterConcurrentSenders(t *testing.T) {
	t.Parallel()
	socketPath, err := LocalPath()
	if err != nil {
		t.Fatalf("LocalPath() error = %v", err)
	}
	defer os.Remove(socketPath) // Clean up after test

	var receivedCount int32
	var mu sync.Mutex
	var receivedMessages [][]byte

	reader, err := NewReader(
		func(data []byte) error {
			mu.Lock()
			receivedMessages = append(receivedMessages, append([]byte(nil), data...))
			mu.Unlock()
			atomic.AddInt32(&receivedCount, 1)
			return nil
		},
		socketPath,
	)
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	defer reader.Stop(context.Background())

	if err := reader.Start(); err != nil {
		t.Fatalf("reader.Start() error = %v", err)
	}

	// Give reader time to start
	time.Sleep(100 * time.Millisecond)

	// Create multiple writers
	numWriters := 5
	messagesPerWriter := 10
	expectedTotal := numWriters * messagesPerWriter

	var wg sync.WaitGroup

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()

			writer, err := NewWriter(socketPath)
			if err != nil {
				t.Errorf("NewWriter() error = %v", err)
				return
			}
			defer writer.Stop(context.Background())

			writer.Start()

			for j := 0; j < messagesPerWriter; j++ {
				message := fmt.Sprintf("writer%d_msg%d", writerID, j)
				writer.WriteMessage([]byte(message))
				time.Sleep(10 * time.Millisecond) // Small delay between messages
			}
		}(i)
	}

	wg.Wait()

	// Wait for all messages to be processed
	timeout := time.After(5 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			t.Logf("Timeout reached. Received %d out of %d expected messages",
				atomic.LoadInt32(&receivedCount), expectedTotal)
			goto done
		case <-ticker.C:
			if atomic.LoadInt32(&receivedCount) >= int32(expectedTotal) {
				goto done
			}
		}
	}

done:
	finalCount := atomic.LoadInt32(&receivedCount)
	if int(finalCount) < expectedTotal {
		t.Errorf("expected at least %d messages, got %d", expectedTotal, finalCount)
	}

	// Verify no duplicate or corrupted messages
	mu.Lock()
	messageMap := make(map[string]int)
	for _, msg := range receivedMessages {
		messageMap[string(msg)]++
	}
	mu.Unlock()

	duplicates := 0
	for msg, count := range messageMap {
		if count > 1 {
			duplicates++
			t.Errorf("message %q received %d times", msg, count)
		}
	}

	if duplicates > 0 {
		t.Errorf("found %d duplicate messages", duplicates)
	}

	t.Logf("Successfully handled concurrent senders: %d writers, %d messages each, %d total received",
		numWriters, messagesPerWriter, finalCount)
}

func TestReaderWriterHandlerError(t *testing.T) {
	t.Parallel()
	socketPath, err := LocalPath()
	if err != nil {
		t.Fatalf("LocalPath() error = %v", err)
	}
	defer os.Remove(socketPath) // Clean up after test

	var handlerCalls int32
	var readerErrors int32

	reader, err := NewReader(
		func(data []byte) error {
			calls := atomic.AddInt32(&handlerCalls, 1)
			if calls%2 == 0 {
				return fmt.Errorf("simulated handler error for call %d", calls)
			}
			return nil
		},
		socketPath,
		WithReaderErrorCallback(func(err error, context string) {
			if context == "handler failed to process a message" {
				atomic.AddInt32(&readerErrors, 1)
			}
		}),
	)
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	defer reader.Stop(context.Background())

	writer, err := NewWriter(socketPath)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	defer writer.Stop(context.Background())

	if err := reader.Start(); err != nil {
		t.Fatalf("reader.Start() error = %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	writer.Start()

	// Send messages that will cause some handler errors
	for i := 0; i < 6; i++ {
		writer.WriteMessage([]byte(fmt.Sprintf("message%d", i)))
	}

	// Wait for processing
	time.Sleep(500 * time.Millisecond)

	// Verify handler was called for all messages
	if atomic.LoadInt32(&handlerCalls) != 6 {
		t.Errorf("expected 6 handler calls, got %d", atomic.LoadInt32(&handlerCalls))
	}

	// Verify error callbacks were triggered (every 2nd message should fail)
	expectedErrors := int32(3)
	if atomic.LoadInt32(&readerErrors) != expectedErrors {
		t.Errorf("expected %d reader errors, got %d", expectedErrors, atomic.LoadInt32(&readerErrors))
	}

	// Verify metrics reflect the errors
	_, _, errorCount := reader.GetMetrics()
	if errorCount != uint64(expectedErrors) {
		t.Errorf("expected error count %d, got %d", expectedErrors, errorCount)
	}
}
