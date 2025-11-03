# Stereo Audio Input Support

## Overview

As of Phase 2B completion, Jivefire now supports both mono and stereo audio input files. Stereo files are automatically downmixed to mono for visualization, ensuring compatibility with the existing audio analysis pipeline.

## Implementation Details

### The extractFloatsWithDownmix Function

The new `extractFloatsWithDownmix` function replaces the previous `extractMonoFloats` function and handles:
- Mono passthrough (unchanged behavior)
- Stereo-to-mono downmixing
- Both interleaved and planar audio formats
- Both 16-bit integer and 32-bit float samples

### Supported Audio Formats

#### Channel Configurations
- **Mono** (1 channel) - Direct passthrough
- **Stereo** (2 channels) - Automatically downmixed to mono
- **Multi-channel** (>2 channels) - Rejected with clear error message

#### Sample Formats
- **Format 1**: 16-bit signed integer (interleaved)
- **Format 3**: 32-bit float (interleaved)
- **Format 6**: 16-bit signed integer (planar)
- **Format 8**: 32-bit float (planar)

### Downmix Algorithm

The stereo-to-mono downmix uses a simple average:
```
mono_sample = (left_sample + right_sample) / 2
```

This preserves the overall energy of the audio signal while ensuring compatibility with the mono-based visualization pipeline.

### Format-Specific Handling

#### Interleaved Stereo
For interleaved formats (1 and 3), samples are arranged as: L R L R L R ...
```go
// 16-bit example
leftVal := int16(data[i*4]) | int16(data[i*4+1])<<8
rightVal := int16(data[i*4+2]) | int16(data[i*4+3])<<8
samples[i] = (float32(leftVal) + float32(rightVal)) / (2 * 32768.0)
```

#### Planar Stereo
For planar formats (6 and 8), left and right channels are in separate buffers:
```go
// Access separate channel buffers
leftPtr := frame.Data().Get(0)
rightPtr := frame.Data().Get(1)
// Average the samples
samples[i] = (leftFloat + rightFloat) / 2
```

## Usage

No changes are required to use stereo files - simply provide them as input:
```bash
./jivefire stereo-audio.wav output.mp4
```

The tool will automatically detect the channel configuration and handle the conversion transparently.

## Testing

The implementation has been tested with:
- Mono WAV files (original functionality preserved)
- Stereo WAV files (both 16-bit and float formats)
- Multi-channel files (properly rejected with error)

## Error Messages

When unsupported audio configurations are detected:
- "unsupported channel count: 6 (only mono and stereo input are supported)"
- "unsupported audio format: 0. Supported formats: 16-bit PCM (1,6) and 32-bit float (3,8)"

## Benefits

1. **Greater Compatibility**: Users can now use stereo audio files without pre-conversion
2. **Automatic Handling**: No manual conversion steps required
3. **Preserved Quality**: Downmixing happens during processing, preserving source quality
4. **Clear Feedback**: Unsupported formats are clearly communicated to users
