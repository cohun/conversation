// PCM Processor AudioWorklet
// Downsamples input audio from audioContext.sampleRate to 16,000 Hz 16-bit linear PCM

class PCMProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
    this.targetSampleRate = 16000;
    this.buffer = [];
    this.isMuted = false;

    this.port.onmessage = (event) => {
      if (event.data && typeof event.data.mute !== 'undefined') {
        this.isMuted = event.data.mute;
      }
    };
  }

  process(inputs, outputs, parameters) {
    if (this.isMuted) {
      return true;
    }

    const input = inputs[0];
    if (!input || !input[0] || input[0].length === 0) {
      return true;
    }

    const channelData = input[0]; // mono channel
    const inputSampleRate = sampleRate; // global in AudioWorklet

    // Calculate volume / RMS for local energy detection
    let sum = 0;
    for (let i = 0; i < channelData.length; i++) {
      sum += channelData[i] * channelData[i];
    }
    const rms = Math.sqrt(sum / channelData.length);

    // Resample to 16kHz
    const resampled = this.downsample(channelData, inputSampleRate, this.targetSampleRate);

    // Convert Float32 [-1.0, 1.0] to Int16 [-32768, 32767]
    const pcm16 = new Int16Array(resampled.length);
    for (let i = 0; i < resampled.length; i++) {
      let s = Math.max(-1, Math.min(1, resampled[i]));
      pcm16[i] = s < 0 ? s * 0x8000 : s * 0x7FFF;
    }

    // Send Int16 buffer and RMS energy to main thread
    this.port.postMessage({
      pcm: pcm16.buffer,
      rms: rms
    }, [pcm16.buffer]);

    return true;
  }

  downsample(buffer, fromRate, toRate) {
    if (fromRate === toRate) {
      return buffer;
    }
    if (fromRate < toRate) {
      return buffer;
    }

    const sampleRateRatio = fromRate / toRate;
    const newLength = Math.round(buffer.length / sampleRateRatio);
    const result = new Float32Array(newLength);

    let offsetResult = 0;
    let offsetBuffer = 0;

    while (offsetResult < result.length) {
      const nextOffsetBuffer = Math.round((offsetResult + 1) * sampleRateRatio);
      let accum = 0;
      let count = 0;
      for (let i = offsetBuffer; i < nextOffsetBuffer && i < buffer.length; i++) {
        accum += buffer[i];
        count++;
      }
      result[offsetResult] = count > 0 ? accum / count : 0;
      offsetResult++;
      offsetBuffer = nextOffsetBuffer;
    }
    return result;
  }
}

registerProcessor('pcm-processor', PCMProcessor);
