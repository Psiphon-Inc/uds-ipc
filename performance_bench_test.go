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

// BenchmarkBasicThroughput measures basic message throughput with 1KB messages
func BenchmarkBasicThroughput(b *testing.B) {
	tempDir := b.TempDir()
	socketPath := filepath.Join(tempDir, "test.sock")

	var received int64
	message := make([]byte, 1024)

	reader, err := NewReader(
		func(data []byte) error {
			atomic.AddInt64(&received, 1)
			return nil
		},
		socketPath,
	)
	if err != nil {
		b.Fatalf("NewReader() error = %v", err)
	}
	defer reader.Stop(context.Background())

	writer, err := NewWriter(socketPath, WithMaxBufferedWrites(uint32(b.N+100)))
	if err != nil {
		b.Fatalf("NewWriter() error = %v", err)
	}
	defer writer.Stop(context.Background())

	if err := reader.Start(); err != nil {
		b.Fatalf("reader.Start() error = %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	writer.Start()
	time.Sleep(10 * time.Millisecond)

	b.ResetTimer()
	b.SetBytes(1024)

	// Send all messages
	for i := 0; i < b.N; i++ {
		writer.WriteMessage(message)
	}

	// Wait for completion with reasonable timeout
	deadline := time.Now().Add(10 * time.Second)
	for atomic.LoadInt64(&received) < int64(b.N) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	b.StopTimer()

	receivedCount := atomic.LoadInt64(&received)
	if receivedCount != int64(b.N) {
		b.Errorf("Only received %d/%d messages", receivedCount, b.N)
	}
}

// BenchmarkMessageSizes tests performance across different message sizes
func BenchmarkMessageSizes(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"1KB", 1024},
		{"4KB", 4 * 1024},
		{"16KB", 16 * 1024},
		{"64KB", 64 * 1024},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			tempDir := b.TempDir()
			socketPath := filepath.Join(tempDir, "test.sock")

			var received int64
			message := make([]byte, size.size)

			reader, err := NewReader(
				func(data []byte) error {
					atomic.AddInt64(&received, 1)
					return nil
				},
				socketPath,
			)
			if err != nil {
				b.Fatalf("NewReader() error = %v", err)
			}
			defer reader.Stop(context.Background())

			writer, err := NewWriter(socketPath, WithMaxBufferedWrites(uint32(b.N+100)))
			if err != nil {
				b.Fatalf("NewWriter() error = %v", err)
			}
			defer writer.Stop(context.Background())

			if err := reader.Start(); err != nil {
				b.Fatalf("reader.Start() error = %v", err)
			}

			time.Sleep(10 * time.Millisecond)
			writer.Start()
			time.Sleep(10 * time.Millisecond)

			b.ResetTimer()
			b.SetBytes(int64(size.size))

			// Send messages
			for i := 0; i < b.N; i++ {
				writer.WriteMessage(message)
			}

			// Wait for completion
			deadline := time.Now().Add(10 * time.Second)
			for atomic.LoadInt64(&received) < int64(b.N) && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}

			b.StopTimer()

			receivedCount := atomic.LoadInt64(&received)
			if receivedCount != int64(b.N) {
				b.Errorf("Only received %d/%d messages", receivedCount, b.N)
			}
		})
	}
}

// BenchmarkBufferOptimization compares system default vs optimized buffer sizes
func BenchmarkBufferOptimization(b *testing.B) {
	configs := []struct {
		name   string
		buffer uint32
	}{
		{"SystemDefault", 0},
		{"Optimized256KB", 256 * 1024},
	}

	for _, config := range configs {
		b.Run(config.name, func(b *testing.B) {
			tempDir := b.TempDir()
			socketPath := filepath.Join(tempDir, "test.sock")

			var received int64
			message := make([]byte, 8192) // 8KB messages

			readerOpts := []ReaderOption{}
			if config.buffer > 0 {
				readerOpts = append(readerOpts, WithReadBufferSize(config.buffer))
			}

			reader, err := NewReader(
				func(data []byte) error {
					atomic.AddInt64(&received, 1)
					return nil
				},
				socketPath,
				readerOpts...,
			)
			if err != nil {
				b.Fatalf("NewReader() error = %v", err)
			}
			defer reader.Stop(context.Background())

			writerOpts := []WriterOption{WithMaxBufferedWrites(uint32(b.N + 100))}
			if config.buffer > 0 {
				writerOpts = append(writerOpts, WithWriteBufferSize(config.buffer))
			}

			writer, err := NewWriter(socketPath, writerOpts...)
			if err != nil {
				b.Fatalf("NewWriter() error = %v", err)
			}
			defer writer.Stop(context.Background())

			if err := reader.Start(); err != nil {
				b.Fatalf("reader.Start() error = %v", err)
			}

			time.Sleep(10 * time.Millisecond)
			writer.Start()
			time.Sleep(10 * time.Millisecond)

			b.ResetTimer()
			b.SetBytes(8192)

			// Send messages
			for i := 0; i < b.N; i++ {
				writer.WriteMessage(message)
			}

			// Wait for completion
			deadline := time.Now().Add(10 * time.Second)
			for atomic.LoadInt64(&received) < int64(b.N) && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}

			b.StopTimer()

			receivedCount := atomic.LoadInt64(&received)
			sent, dropped, failed, _ := writer.GetMetrics()

			if receivedCount != int64(b.N) {
				b.Errorf("Only received %d/%d messages", receivedCount, b.N)
			}

			b.ReportMetric(float64(dropped), "msgs_dropped")
			b.ReportMetric(float64(failed), "msgs_failed")
			b.ReportMetric(float64(sent), "msgs_sent")
		})
	}
}

// BenchmarkMultipleWriters tests performance with multiple concurrent writers
func BenchmarkMultipleWriters(b *testing.B) {
	writerCounts := []int{1, 2, 4, 8}

	for _, writerCount := range writerCounts {
		b.Run(string(rune('0'+writerCount))+"Writers", func(b *testing.B) {
			tempDir := b.TempDir()
			socketPath := filepath.Join(tempDir, "test.sock")

			var received int64
			message := make([]byte, 1024)

			reader, err := NewReader(
				func(data []byte) error {
					atomic.AddInt64(&received, 1)
					return nil
				},
				socketPath,
			)
			if err != nil {
				b.Fatalf("NewReader() error = %v", err)
			}
			defer reader.Stop(context.Background())

			// Create multiple writers
			writers := make([]*Writer, writerCount)
			for i := 0; i < writerCount; i++ {
				writer, err := NewWriter(socketPath, WithMaxBufferedWrites(uint32(b.N/writerCount+100)))
				if err != nil {
					b.Fatalf("NewWriter() error = %v", err)
				}
				writers[i] = writer
				defer writer.Stop(context.Background())
			}

			if err := reader.Start(); err != nil {
				b.Fatalf("reader.Start() error = %v", err)
			}

			time.Sleep(10 * time.Millisecond)
			for _, writer := range writers {
				writer.Start()
			}
			time.Sleep(10 * time.Millisecond)

			b.ResetTimer()
			b.SetBytes(1024)

			// Send messages from each writer
			messagesPerWriter := b.N / writerCount
			for i := 0; i < messagesPerWriter; i++ {
				for _, writer := range writers {
					writer.WriteMessage(message)
				}
			}

			// Wait for completion
			expectedMessages := int64(messagesPerWriter * writerCount)
			deadline := time.Now().Add(10 * time.Second)
			for atomic.LoadInt64(&received) < expectedMessages && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}

			b.StopTimer()

			receivedCount := atomic.LoadInt64(&received)
			if receivedCount != expectedMessages {
				b.Errorf("Only received %d/%d messages", receivedCount, expectedMessages)
			}
		})
	}
}

// BenchmarkWriteOnly tests just the write operation speed without reading
func BenchmarkWriteOnly(b *testing.B) {
	tempDir := b.TempDir()
	socketPath := filepath.Join(tempDir, "test.sock")

	// Create a reader that just discards messages quickly
	reader, err := NewReader(
		func(data []byte) error { return nil },
		socketPath,
	)
	if err != nil {
		b.Fatalf("NewReader() error = %v", err)
	}
	defer reader.Stop(context.Background())

	writer, err := NewWriter(socketPath, WithMaxBufferedWrites(uint32(b.N+1000)))
	if err != nil {
		b.Fatalf("NewWriter() error = %v", err)
	}
	defer writer.Stop(context.Background())

	if err := reader.Start(); err != nil {
		b.Fatalf("reader.Start() error = %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	writer.Start()
	time.Sleep(10 * time.Millisecond)

	message := make([]byte, 1024)

	b.ResetTimer()
	b.SetBytes(1024)

	for i := 0; i < b.N; i++ {
		writer.WriteMessage(message)
	}

	b.StopTimer()

	sent, dropped, failed, queueDepth := writer.GetMetrics()
	b.ReportMetric(float64(dropped), "msgs_dropped")
	b.ReportMetric(float64(failed), "msgs_failed")
	b.ReportMetric(float64(queueDepth), "queue_depth")
	b.ReportMetric(float64(sent), "msgs_sent")
}

// BenchmarkReadOnly measures pure reader performance by pre-filling messages
func BenchmarkReadOnly(b *testing.B) {
	tempDir := b.TempDir()
	socketPath := filepath.Join(tempDir, "test.sock")

	var received int64
	done := make(chan struct{}, 1)
	message := make([]byte, 1024)

	reader, err := NewReader(
		func(data []byte) error {
			count := atomic.AddInt64(&received, 1)
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

	writer, err := NewWriter(socketPath, WithMaxBufferedWrites(uint32(b.N+100)))
	if err != nil {
		b.Fatalf("NewWriter() error = %v", err)
	}
	defer writer.Stop(context.Background())

	if err := reader.Start(); err != nil {
		b.Fatalf("reader.Start() error = %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	writer.Start()
	time.Sleep(10 * time.Millisecond)

	// Pre-fill all messages before starting the timer
	for i := 0; i < b.N; i++ {
		writer.WriteMessage(message)
	}

	b.ResetTimer()
	b.SetBytes(1024)

	// Wait for all messages to be processed (pure read performance)
	select {
	case <-done:
		// All messages received
	case <-time.After(30 * time.Second):
		b.Fatalf("timeout waiting for messages: received %d/%d", atomic.LoadInt64(&received), b.N)
	}

	b.StopTimer()

	// Report metrics
	receivedCount, connections, errors := reader.GetMetrics()
	b.ReportMetric(float64(errors), "read_errors")
	b.ReportMetric(float64(connections), "connections")
	b.ReportMetric(float64(receivedCount), "msgs_received")
}
