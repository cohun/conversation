// PCM Processor AudioWorklet
// Converts audio to studio-quality 16,000 Hz 16-bit linear PCM with continuous phase interpolation

class PCMProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
    this.targetSampleRate = 16000;
    this.chunkSize = 1600; // 100ms at 16kHz
    this.buffer = new Int16Array(this.chunkSize);
    this.bufferIndex = 0;
    this.isMuted = false;
    this.phase = 0.0;

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

    const channelData = input[0]; // mono Float32 [-1.0, 1.0]
    const inputSampleRate = sampleRate; // global in AudioWorklet

    // Calculate RMS energy
    let sum = 0;
    for (let i = 0; i < channelData.length; i++) {
      sum += channelData[i] * channelData[i];
    }
    const rms = Math.sqrt(sum / channelData.length);

    // Fast path: if audioContext is already 16kHz, direct conversion with 0 resampling
    if (inputSampleRate === this.targetSampleRate) {
      for (let i = 0; i < channelData.length; i++) {
        let s = Math.max(-1, Math.min(1, channelData[i]));
        const sample16 = s < 0 ? s * 0x8000 : s * 0x7FFF;
        this.buffer[this.bufferIndex++] = sample16;

        if (this.bufferIndex >= this.chunkSize) {
          const pcmCopy = new Int16Array(this.buffer);
          this.port.postMessage({
            pcm: pcmCopy.buffer,
            rms: rms,
          }, [pcmCopy.buffer]);
          this.bufferIndex = 0;
        }
      }
      return true;
    }

    // Continuous linear interpolation resampler with persistent phase across quanta
    const ratio = inputSampleRate / this.targetSampleRate;
    while (this.phase < channelData.length) {
      const i0 = Math.floor(this.phase);
      const i1 = Math.min(channelData.length - 1, i0 + 1);
      const frac = this.phase - i0;
      const s = channelData[i0] + frac * (channelData[i1] - channelData[i0]);

      const clamped = Math.max(-1, Math.min(1, s));
      const sample16 = clamped < 0 ? clamped * 0x8000 : clamped * 0x7FFF;
      this.buffer[this.bufferIndex++] = sample16;

      if (this.bufferIndex >= this.chunkSize) {
        const pcmCopy = new Int16Array(this.buffer);
        this.port.postMessage({
          pcm: pcmCopy.buffer,
          rms: rms,
        }, [pcmCopy.buffer]);
        this.bufferIndex = 0;
      }

      this.phase += ratio;
    }
    this.phase -= channelData.length;

    return true;
  }
}

registerProcessor('pcm-processor', PCMProcessor);
