# Phase 2B Implementation Status

## Summary

Phase 2B aimed to implement full audio encoding within ffmpeg-go to eliminate the external FFmpeg dependency for audio muxing. While significant progress was made, we encountered technical limitations that prevent complete implementation at this time.

## What Was Accomplished

1. **Audio Input Support Added to Encoder**
   - Added `AudioPath` field to encoder Config
   - Implemented `initializeAudio()` method to open and parse WAV files
   - Set up audio decoder context for WAV input
   - Created audio encoder context for AAC output

2. **Audio Processing Pipeline**
   - Implemented `ProcessAudio()` method with full decode/encode pipeline
   - Added proper packet reading and frame decoding
   - Integrated audio stream into the output MP4 container

3. **Resource Management**
   - Added cleanup for audio contexts in `Close()` method
   - Proper allocation and deallocation of audio frames and packets

## Technical Challenges Encountered

### 1. Channel Layout Incompatibility
- **Issue**: AAC encoder doesn't support mono audio (1 channel)
- **Attempted Solution**: Force stereo output with channel layout = 3 (AV_CH_LAYOUT_STEREO)
- **Result**: Channel configuration accepted but leads to next issue

### 2. Frame Size Mismatch
- **Issue**: WAV decoder produces 2048 samples per frame, but AAC encoder expects 1024 samples
- **Error**: `nb_samples (2048) > frame_size (1024)`
- **Root Cause**: Different codecs have different frame size requirements

### 3. Missing Audio Resampling API
- **Issue**: ffmpeg-go v0.6.0 doesn't expose libswresample functionality
- **Impact**: Cannot properly:
  - Convert between different sample formats
  - Change channel layouts (mono to stereo)
  - Adjust frame sizes between decoder and encoder

## What Would Be Required for Full Implementation

1. **libswresample Bindings**
   - Need SwrContext allocation and configuration
   - swr_convert() for audio resampling
   - Proper handling of different frame sizes

2. **FIFO Buffer Implementation**
   - Audio FIFO to accumulate samples
   - Logic to feed encoder with correct frame sizes
   - Handle partial frames at the end of stream

3. **Manual Channel Duplication**
   - If swresample not available, manually duplicate mono to stereo
   - Handle different sample formats (planar vs interleaved)

## Current Workaround

The application continues to use the Phase 2A approach:
1. Use ffmpeg-go to encode video-only MP4
2. Use external FFmpeg command to mux video with audio
3. This ensures correct audio handling without complex resampling

## Code Structure

The audio encoding infrastructure is in place in `internal/encoder/encoder.go`:
- `initializeAudio()` - Sets up audio input and codecs
- `ProcessAudio()` - Reads, decodes, and attempts to encode audio
- Audio-related fields added to Encoder struct

This code can be activated by:
1. Setting `AudioPath` in encoder.Config
2. Calling `ProcessAudio()` after video encoding
3. Handling the frame size mismatch issue

## Recommendations

1. **Short Term**: Continue using external FFmpeg for audio muxing
2. **Long Term Options**:
   - Wait for ffmpeg-go to expose swresample API
   - Contribute swresample bindings to ffmpeg-go
   - Implement a custom audio resampling solution
   - Use a different FFmpeg binding library with fuller API coverage

## Testing

To test the current implementation:
```bash
# This will fail with frame size error
./jivefire testdata/dream.wav testdata/test.mp4
```

The error demonstrates that the audio pipeline is working up to the point of encoding, where the frame size mismatch occurs.