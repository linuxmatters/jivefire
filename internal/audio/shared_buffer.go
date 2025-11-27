package audio

import (
	"errors"
	"sync"
)

// ErrBufferClosed is returned when attempting to read from a closed buffer
var ErrBufferClosed = errors.New("buffer is closed")

// SharedAudioBuffer provides a thread-safe buffer for sharing decoded audio
// between FFT analysis and AAC encoding. Each consumer has an independent
// read position, allowing them to consume data at different rates.
//
// Design:
// - Single producer (decoder) writes samples via Write()
// - Two independent consumers: FFT (ReadForFFT) and encoder (ReadForEncoder)
// - Each consumer tracks its own read position
// - Buffer automatically expands as needed (no ring buffer complexity)
// - EOF signalling via Close() propagates to both consumers
type SharedAudioBuffer struct {
	mu sync.Mutex

	// Underlying sample storage (float64 for FFT precision)
	samples []float64

	// Independent read positions for each consumer
	fftReadPos     int
	encoderReadPos int

	// EOF signalling
	closed bool

	// Condition variable for blocking reads
	cond *sync.Cond
}

// NewSharedAudioBuffer creates a new shared audio buffer.
// initialCapacity is a hint for the expected total samples (can grow as needed).
func NewSharedAudioBuffer(initialCapacity int) *SharedAudioBuffer {
	if initialCapacity <= 0 {
		initialCapacity = 1024 * 1024 // 1M samples default (~23 seconds at 44.1kHz)
	}

	b := &SharedAudioBuffer{
		samples: make([]float64, 0, initialCapacity),
	}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// Write appends samples to the buffer. This is called by the decoder.
// Signals waiting consumers when new data is available.
func (b *SharedAudioBuffer) Write(samples []float64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return ErrBufferClosed
	}

	b.samples = append(b.samples, samples...)
	b.cond.Broadcast() // Wake up any waiting consumers
	return nil
}

// ReadForFFT reads samples for FFT analysis.
// Returns up to numSamples, or fewer if not enough available.
// Non-blocking: returns immediately with available samples (may be empty).
// Returns io.EOF when buffer is closed and all samples have been read.
func (b *SharedAudioBuffer) ReadForFFT(numSamples int) ([]float64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	available := len(b.samples) - b.fftReadPos
	if available <= 0 {
		if b.closed {
			return nil, ErrBufferClosed
		}
		return nil, nil // No samples available yet
	}

	// Read up to numSamples
	toRead := numSamples
	if toRead > available {
		toRead = available
	}

	result := make([]float64, toRead)
	copy(result, b.samples[b.fftReadPos:b.fftReadPos+toRead])
	b.fftReadPos += toRead

	return result, nil
}

// ReadForEncoder reads samples for AAC encoding.
// Returns exactly numSamples, converted to float32.
// Blocks until enough samples are available or buffer is closed.
// Returns io.EOF when buffer is closed and all samples have been read.
func (b *SharedAudioBuffer) ReadForEncoder(numSamples int) ([]float32, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for {
		available := len(b.samples) - b.encoderReadPos

		// If we have enough samples, return them
		if available >= numSamples {
			result := make([]float32, numSamples)
			for i := 0; i < numSamples; i++ {
				result[i] = float32(b.samples[b.encoderReadPos+i])
			}
			b.encoderReadPos += numSamples
			return result, nil
		}

		// Buffer closed - return whatever we have
		if b.closed {
			if available <= 0 {
				return nil, ErrBufferClosed
			}
			// Return remaining samples (partial frame)
			result := make([]float32, available)
			for i := 0; i < available; i++ {
				result[i] = float32(b.samples[b.encoderReadPos+i])
			}
			b.encoderReadPos += available
			return result, nil
		}

		// Wait for more samples
		b.cond.Wait()
	}
}

// ReadForEncoderNonBlocking reads samples for encoding without blocking.
// Returns nil if not enough samples are available yet.
// This is useful for the interleaved encode pattern where we only want
// to encode if we have enough audio to match the video PTS.
func (b *SharedAudioBuffer) ReadForEncoderNonBlocking(numSamples int) ([]float32, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	available := len(b.samples) - b.encoderReadPos

	// If we have enough samples, return them
	if available >= numSamples {
		result := make([]float32, numSamples)
		for i := 0; i < numSamples; i++ {
			result[i] = float32(b.samples[b.encoderReadPos+i])
		}
		b.encoderReadPos += numSamples
		return result, nil
	}

	// Buffer closed - return whatever we have
	if b.closed && available > 0 {
		result := make([]float32, available)
		for i := 0; i < available; i++ {
			result[i] = float32(b.samples[b.encoderReadPos+i])
		}
		b.encoderReadPos += available
		return result, nil
	}

	if b.closed {
		return nil, ErrBufferClosed
	}

	// Not enough samples yet
	return nil, nil
}

// AvailableForFFT returns the number of unread samples for FFT.
func (b *SharedAudioBuffer) AvailableForFFT() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.samples) - b.fftReadPos
}

// AvailableForEncoder returns the number of unread samples for encoder.
func (b *SharedAudioBuffer) AvailableForEncoder() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.samples) - b.encoderReadPos
}

// TotalSamples returns the total number of samples written to the buffer.
func (b *SharedAudioBuffer) TotalSamples() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.samples)
}

// Close signals that no more samples will be written.
// Wakes up any blocked consumers.
func (b *SharedAudioBuffer) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.closed = true
	b.cond.Broadcast() // Wake up any waiting consumers
}

// IsClosed returns whether the buffer has been closed.
func (b *SharedAudioBuffer) IsClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

// Reset clears the buffer and resets read positions.
// Useful for reusing the buffer for a new audio file.
func (b *SharedAudioBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.samples = b.samples[:0]
	b.fftReadPos = 0
	b.encoderReadPos = 0
	b.closed = false
}

// CompactBuffer removes samples that have been consumed by both consumers.
// This frees memory for long audio files. Call periodically during processing.
func (b *SharedAudioBuffer) CompactBuffer() {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Find the minimum read position (samples consumed by both)
	minPos := b.fftReadPos
	if b.encoderReadPos < minPos {
		minPos = b.encoderReadPos
	}

	if minPos == 0 {
		return // Nothing to compact
	}

	// Shift samples and adjust positions
	remaining := len(b.samples) - minPos
	copy(b.samples, b.samples[minPos:])
	b.samples = b.samples[:remaining]
	b.fftReadPos -= minPos
	b.encoderReadPos -= minPos
}
