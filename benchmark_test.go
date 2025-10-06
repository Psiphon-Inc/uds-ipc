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
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// BenchmarkReaderWriter measures end-to-end throughput
func BenchmarkReaderWriter(b *testing.B) {
	benchmarks := []struct {
		name        string
		messageSize int
		bufferSize  uint32
	}{
		{"SmallMessages_1KB_SmallBuffer", 1024, 1000},
		{"SmallMessages_1KB_LargeBuffer", 1024, 10000},
		{"MediumMessages_10KB_SmallBuffer", 10240, 1000},
		{"MediumMessages_10KB_LargeBuffer", 10240, 10000},
		{"LargeMessages_100KB_SmallBuffer", 102400, 1000},
		{"LargeMessages_100KB_LargeBuffer", 102400, 10000},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			benchmarkReaderWriter(b, bm.messageSize, bm.bufferSize)
		})
	}
}

func benchmarkReaderWriter(b *testing.B, messageSize int, bufferSize uint32) {
	tempDir := b.TempDir()
	socketPath := filepath.Join(tempDir, "test.sock")

	// Fix buffer size to handle b.N messages
	effectiveBufferSize := bufferSize
	if uint32(b.N) > bufferSize {
		effectiveBufferSize = uint32(b.N + 100)
	}

	var receivedCount int64
	done := make(chan struct{}, 1)
	message := make([]byte, messageSize)
	for i := range message {
		message[i] = byte(i % 256)
	}

	reader, err := NewReader(
		func(data []byte) error {
			count := atomic.AddInt64(&receivedCount, 1)
			if count >= int64(b.N) {
				select {
				case done <- struct{}{}:
				default:
				}
			}
			return nil
		},
		socketPath,
	)
	if err != nil {
		b.Fatalf("NewReader() error = %v", err)
	}
	defer reader.Stop(context.Background())

	writer, err := NewWriter(
		socketPath,
		WithMaxBufferedWrites(effectiveBufferSize),
	)
	if err != nil {
		b.Fatalf("NewWriter() error = %v", err)
	}
	defer writer.Stop(context.Background())

	if err := reader.Start(); err != nil {
		b.Fatalf("reader.Start() error = %v", err)
	}

	// Give reader time to start
	time.Sleep(10 * time.Millisecond)
	writer.Start()

	b.ResetTimer()
	b.SetBytes(int64(messageSize))

	for i := 0; i < b.N; i++ {
		writer.WriteMessage(message)
	}

	// Wait for all messages to be processed with proper synchronization
	select {
	case <-done:
		// All messages received
	case <-time.After(30 * time.Second):
		b.Fatalf("timeout waiting for messages: sent %d, received %d", b.N, atomic.LoadInt64(&receivedCount))
	}

	b.StopTimer()

	// Report additional metrics
	sent, dropped, failed, _ := writer.GetMetrics()
	b.Logf("Writer metrics: sent=%d, dropped=%d, failed=%d", sent, dropped, failed)

	received, connections, errors := reader.GetMetrics()
	b.Logf("Reader metrics: received=%d, connections=%d, errors=%d", received, connections, errors)
}

// BenchmarkHandlerLatency measures handler processing latency
func BenchmarkHandlerLatency(b *testing.B) {
	benchmarks := []struct {
		name         string
		handlerDelay time.Duration
		messageSize  int
		maxOps       int // Limit operations for delay benchmarks to prevent timeouts
	}{
		{"NoDelay_1KB", 0, 1024, 0},                       // 0 means no limit, use b.N
		{"10us_1KB", 10 * time.Microsecond, 1024, 10000},  // Limit to 10k ops (100ms)
		{"100us_1KB", 100 * time.Microsecond, 1024, 1000}, // Limit to 1k ops (100ms)
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			benchmarkHandlerLatency(b, bm.handlerDelay, bm.messageSize, bm.maxOps)
		})
	}
}

func benchmarkHandlerLatency(b *testing.B, handlerDelay time.Duration, messageSize int, maxOps int) {
	tempDir := b.TempDir()
	socketPath := filepath.Join(tempDir, "test.sock")

	// Determine actual number of operations to run
	numOps := b.N
	if maxOps > 0 && numOps > maxOps {
		numOps = maxOps
	}

	var receivedCount int64
	done := make(chan struct{}, 1)
	message := make([]byte, messageSize)

	reader, err := NewReader(
		func(data []byte) error {
			if handlerDelay > 0 {
				time.Sleep(handlerDelay)
			}
			count := atomic.AddInt64(&receivedCount, 1)
			if count >= int64(numOps) {
				select {
				case done <- struct{}{}:
				default:
				}
			}
			return nil
		},
		socketPath,
	)
	if err != nil {
		b.Fatalf("NewReader() error = %v", err)
	}
	defer reader.Stop(context.Background())

	// Use appropriate buffer size for handler latency test
	bufferSize := uint32(numOps + 100)
	writer, err := NewWriter(socketPath, WithMaxBufferedWrites(bufferSize))
	if err != nil {
		b.Fatalf("NewWriter() error = %v", err)
	}
	defer writer.Stop(context.Background())

	if err := reader.Start(); err != nil {
		b.Fatalf("reader.Start() error = %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	writer.Start()

	b.ResetTimer()
	b.SetBytes(int64(messageSize))

	for i := 0; i < numOps; i++ {
		writer.WriteMessage(message)
	}

	// Wait for all messages to be processed with proper synchronization
	// Use reasonable timeout based on actual operations, not b.N
	timeout := 10 * time.Second
	if handlerDelay > 0 {
		// Calculate expected time: numOps * handlerDelay + buffer time
		expectedTime := time.Duration(numOps) * handlerDelay
		timeout = expectedTime + 5*time.Second // Shorter buffer time
	}

	select {
	case <-done:
		// All messages received
	case <-time.After(timeout):
		b.Fatalf("timeout waiting for messages: sent %d, received %d", numOps, atomic.LoadInt64(&receivedCount))
	}

	b.StopTimer()
}

// BenchmarkMemoryAllocation measures memory allocations
func BenchmarkMemoryAllocation(b *testing.B) {
	tempDir := b.TempDir()
	socketPath := filepath.Join(tempDir, "test.sock")

	message := make([]byte, 1024)

	reader, err := NewReader(
		func(data []byte) error { return nil },
		socketPath,
	)
	if err != nil {
		b.Fatalf("NewReader() error = %v", err)
	}
	defer reader.Stop(context.Background())

	// Use buffer size that can handle b.N messages
	bufferSize := uint32(max(10000, b.N+100))
	writer, err := NewWriter(
		socketPath,
		WithMaxBufferedWrites(bufferSize),
	)
	if err != nil {
		b.Fatalf("NewWriter() error = %v", err)
	}
	defer writer.Stop(context.Background())

	if err := reader.Start(); err != nil {
		b.Fatalf("reader.Start() error = %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	writer.Start()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		writer.WriteMessage(message)
	}
}

// BenchmarkProtocolOverhead measures the overhead of the length-prefix protocol
func BenchmarkProtocolOverhead(b *testing.B) {
	benchmarks := []struct {
		name        string
		messageSize int
	}{
		{"1Byte", 1},
		{"100Bytes", 100},
		{"1KB", 1024},
		{"10KB", 10240},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			benchmarkProtocolOverhead(b, bm.messageSize)
		})
	}
}

func benchmarkProtocolOverhead(b *testing.B, messageSize int) {
	tempDir := b.TempDir()
	socketPath := filepath.Join(tempDir, "test.sock")

	var totalBytes int64
	var receivedCount int64
	done := make(chan struct{}, 1)
	message := make([]byte, messageSize)

	reader, err := NewReader(
		func(data []byte) error {
			atomic.AddInt64(&totalBytes, int64(len(data)))
			count := atomic.AddInt64(&receivedCount, 1)
			if count >= int64(b.N) {
				select {
				case done <- struct{}{}:
				default:
				}
			}
			return nil
		},
		socketPath,
	)
	if err != nil {
		b.Fatalf("NewReader() error = %v", err)
	}
	defer reader.Stop(context.Background())

	// Use appropriate buffer size
	bufferSize := uint32(max(1000, b.N+100))
	writer, err := NewWriter(socketPath, WithMaxBufferedWrites(bufferSize))
	if err != nil {
		b.Fatalf("NewWriter() error = %v", err)
	}
	defer writer.Stop(context.Background())

	if err := reader.Start(); err != nil {
		b.Fatalf("reader.Start() error = %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	writer.Start()

	b.ResetTimer()
	b.SetBytes(int64(messageSize))

	for i := 0; i < b.N; i++ {
		writer.WriteMessage(message)
	}

	// Wait for all messages to be processed
	select {
	case <-done:
		// All messages received
	case <-time.After(30 * time.Second):
		b.Fatalf("timeout waiting for messages: sent %d, received %d", b.N, atomic.LoadInt64(&receivedCount))
	}

	b.StopTimer()

	// Calculate protocol overhead
	expectedBytes := int64(b.N * messageSize)
	actualBytes := atomic.LoadInt64(&totalBytes)

	b.Logf("Expected payload bytes: %d", expectedBytes)
	b.Logf("Actual payload bytes: %d", actualBytes)

	if expectedBytes > 0 {
		overhead := float64(actualBytes-expectedBytes) / float64(expectedBytes) * 100
		b.Logf("Protocol overhead: %.2f%%", overhead)
	}
}
