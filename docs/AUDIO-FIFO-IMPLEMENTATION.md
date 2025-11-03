# Pure Go Audio FIFO Implementation

## Overview

This document describes the pure Go audio FIFO (First In, First Out) buffer implementation developed for Phase 2B of the Jivefire project. This implementation was created to handle audio frame size mismatches between the decoder (2048 samples) and AAC encoder (1024 samples) without requiring FFmpeg's libswresample library.

## Background

### The Problem

- **Decoder output**: 2048 samples per frame (typical for WAV files)
- **AAC encoder requirement**: Exactly 1024 samples per frame
- **Channel mismatch**: Input is mono, AAC encoder requires stereo
- **Sample format**: WAV files may contain 16-bit integers or 32-bit floats, but the encoder expects float32

### Why Pure Go?

The ffmpeg-go bindings (v0.6.0) don't include the swresample API, which would normally handle:
- Sample rate conversion
- Channel layout conversion (mono to stereo)
- Sample format conversion
- Frame size adaptation

Rather than:
1. Implementing full swresample bindings (2-3 days of work)
2. Using CGO to wrap just the needed functions (4-6 hours)

We chose a pure Go implementation that took approximately 2 hours to complete.

## Implementation Details

### AudioFIFO Structure

```go
type AudioFIFO struct {
    buffer []float32
    size   int
}
```

The FIFO buffer stores audio samples as `float32` values, which is the format required by the AAC encoder.

### Key Methods

#### Push
```go
func (f *AudioFIFO) Push(samples []float32)
```
Adds samples to the end of the buffer. The buffer automatically grows as needed.

#### Pop
```go
func (f *AudioFIFO) Pop(count int) []float32
```
Removes and returns the requested number of samples from the front of the buffer. Returns `nil` if insufficient samples are available.

#### Size
```go
func (f *AudioFIFO) Size() int
```
Returns the current number of samples in the buffer.

### Audio Processing Pipeline

1. **Decode audio frame** (2048 mono samples)
2. **Extract and convert samples** to float32
3. **Push to FIFO buffer**
4. **Pop exactly 1024 samples** when available
5. **Convert mono to stereo** (duplicate each sample)
6. **Encode with AAC**

### Sample Format Handling

The implementation detects the input sample format and converts accordingly:

```go
switch format {
case 1: // AVSampleFmtS16 - 16-bit signed integer
    // Convert from int16 to float32
    samples[i] = float32(int16(binary.LittleEndian.Uint16(data[i*2:]))) / 32768.0
case 3: // AVSampleFmtFlt - 32-bit float
    // Direct binary conversion
    samples[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
}
```

### Mono to Stereo Conversion

The encoder performs channel duplication:

```go
// Each mono sample becomes L and R channel
for i := 0; i < frameSize; i++ {
    offset := i * 2 * 4  // 2 channels × 4 bytes per float32
    binary.LittleEndian.PutUint32(leftData[offset:], math.Float32bits(samples[i]))
    binary.LittleEndian.PutUint32(rightData[offset:], math.Float32bits(samples[i]))
}
```

## Input Validation

The implementation includes comprehensive validation:

### Supported Formats
- **Sample Format 1**: 16-bit signed integer (PCM)
- **Sample Format 3**: 32-bit float
- **Sample Format 6**: 16-bit signed integer planar
- **Sample Format 8**: 32-bit float planar

### Requirements
- **Channels**: Mono only (1 channel)
- **Sample Rate**: 8,000 Hz to 192,000 Hz

### Error Messages
The validator provides user-friendly error messages:
- "unsupported audio format: 8-bit unsigned (format 0). Supported formats: 16-bit PCM and 32-bit float"
- "unsupported channel count: 2 (only mono input is supported)"
- "unsupported sample rate: 4000Hz (must be between 8kHz and 192kHz)"

## Performance Characteristics

- **Memory efficiency**: Single buffer with efficient slice operations
- **Zero-copy where possible**: Direct pointer access to frame data
- **Minimal allocations**: Reuses buffers across frames
- **CPU usage**: Negligible compared to video encoding

## Edge Cases Handled

1. **Partial frames**: FIFO buffers incomplete frames until enough samples accumulate
2. **Format detection**: Automatically detects 16-bit vs 32-bit samples
3. **Endianness**: Properly handles little-endian byte order
4. **Final frame**: Processes remaining samples at end of file

## Debugging the Garbled Audio Issue

During implementation, we encountered garbled audio characterized by:
- High-pitched clicking
- Incredibly static voice
- Generally distorted output

**Root cause**: The code assumed float32 samples, but WAV files typically contain 16-bit integers.

**Solution**: Added format detection and proper conversion from int16 to float32 with scaling by 32768.

## Alternative Approaches Considered

1. **Full swresample bindings**
   - Pros: Complete feature set, battle-tested
   - Cons: 2-3 days implementation time

2. **CGO wrapper for specific functions**
   - Pros: Direct FFmpeg integration
   - Cons: 4-6 hours implementation, CGO complexity

3. **External resampling library**
   - Pros: Potentially simpler API
   - Cons: Additional dependency, may not exist for Go

## Code Location

The implementation is located in:
- `/internal/encoder/encoder.go` - AudioFIFO struct and methods
- `/internal/encoder/encoder.go` - `extractMonoFloats()` and `writeStereoFloats()` functions

## Testing

The implementation has been tested with:
- 16-bit PCM WAV files ✓
- Various sample rates (8kHz, 44.1kHz, 48kHz) ✓
- Long duration files (>1 minute) ✓
- Short files (<1 second) ✓

## Future Improvements

1. **Sample rate conversion**: Currently relies on encoder to handle rate differences
2. **Multi-channel input**: Could extend to handle stereo input with mixing
3. **Planar format optimization**: Direct handling without intermediate conversion
4. **Dynamic buffer sizing**: Adaptive initial capacity based on file size

## Conclusion

The pure Go FIFO implementation successfully solves the frame size mismatch problem while providing a clean, maintainable solution that avoids complex FFmpeg bindings. The 2-hour implementation time proved the viability of this approach for similar audio processing challenges.
