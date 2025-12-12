package main

import (
	"errors"
)

type LogBuffer struct {
	buffer   []string // Circular buffer storing messages
	capacity int      // Maximum number of messages to store (limit + offset)
	size     int      // Current number of messages in buffer
	start    int      // Index of oldest message
	end      int      // Index where next message will be inserted
	isFull   bool     // Whether buffer is full
}

// NewLogBuffer creates a new circular buffer with given capacity
func NewLogBuffer(capacity int) *LogBuffer {
	if capacity <= 0 {
		capacity = 100 // Default capacity
	}
	return &LogBuffer{
		buffer:   make([]string, capacity),
		capacity: capacity,
		size:     0,
		start:    0,
		end:      0,
		isFull:   false,
	}
}

// Push adds a new message to the buffer, overwriting oldest if full
func (b *LogBuffer) Push(message string) {
	b.buffer[b.end] = message

	if b.isFull {
		// Buffer is full, overwrite oldest
		b.start = (b.start + 1) % b.capacity
	} else {
		b.size++
	}

	b.end = (b.end + 1) % b.capacity

	// Mark as full if we've wrapped around
	if b.end == b.start {
		b.isFull = true
	}
}

// Get retrieves messages with given offset and limit
// offset: number of messages to skip from the newest (0 = newest message)
// limit: maximum number of messages to return
// Returns messages from oldest to newest in the result
func (b *LogBuffer) Get(offset, limit int) ([]string, error) {
	if offset < 0 || limit <= 0 {
		return nil, errors.New("offset must be >= 0 and limit must be > 0")
	}

	if b.size == 0 {
		return []string{}, nil
	}

	// Calculate total messages available after offset
	totalAvailable := b.size - offset
	if totalAvailable <= 0 {
		return []string{}, nil
	}

	// Determine how many messages to return
	count := limit
	if totalAvailable < limit {
		count = totalAvailable
	}

	// Prepare result slice
	result := make([]string, 0, count)

	// Start from the position that's 'offset' from the newest
	// Newest is at (b.end - 1) mod capacity, but we need to handle wrap-around
	newestIndex := (b.end - 1 + b.capacity) % b.capacity

	// Calculate starting index (offset from newest)
	// We need to go backwards 'offset' positions from newest
	startIndex := newestIndex
	for i := 0; i < offset; i++ {
		startIndex = (startIndex - 1 + b.capacity) % b.capacity
	}

	// Now collect 'count' messages moving forward from startIndex
	// But note: we want them in chronological order (oldest to newest)
	// So we need to find the actual chronological start

	// Calculate chronological start index (oldest of the range we want)
	// We have startIndex which is offset from newest, but we want the chronological
	// start which is 'count-1' positions before this in time
	chronoStart := startIndex
	for i := 0; i < (count - 1); i++ {
		chronoStart = (chronoStart - 1 + b.capacity) % b.capacity
	}

	// Now collect messages in chronological order
	for i := 0; i < count; i++ {
		idx := (chronoStart + i) % b.capacity
		result = append(result, b.buffer[idx])
	}

	return result, nil
}

// GetAll returns all messages in chronological order (oldest to newest)
func (b *LogBuffer) GetAll() []string {
	if b.size == 0 {
		return []string{}
	}

	result := make([]string, b.size)

	if b.isFull || b.start < b.end {
		// Simple case: buffer is contiguous
		copy(result, b.buffer[b.start:b.end])
	} else {
		// Buffer wraps around
		firstPart := b.buffer[b.start:]
		secondPart := b.buffer[:b.end]
		copy(result, firstPart)
		copy(result[len(firstPart):], secondPart)
	}

	return result
}

// Size returns current number of messages in buffer
func (b *LogBuffer) Size() int {
	return b.size
}

// Capacity returns maximum capacity of buffer
func (b *LogBuffer) Capacity() int {
	return b.capacity
}

// Clear removes all messages from buffer
func (b *LogBuffer) Clear() {
	b.size = 0
	b.start = 0
	b.end = 0
	b.isFull = false
}
