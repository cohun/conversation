// Antigravity Voice Call Manager
// Handles Web Audio API, AudioWorklet (16kHz PCM), 24kHz PCM Playback, Speech Synthesis & Recognition, WebSocket, and Barge-In

class AudioCallManager {
  constructor(options = {}) {
    this.personaId = options.personaId || 'teacher-sarah';
    this.resumeId = options.resumeId || '';
    this.apiKey = options.apiKey || '';

    // Callbacks
    this.onStateChange = options.onStateChange || (() => {});
    this.onTimerUpdate = options.onTimerUpdate || (() => {});
    this.onTopicSaved = options.onTopicSaved || (() => {});
    this.onError = options.onError || (() => {});
    this.onCallEnded = options.onCallEnded || (() => {});

    // Audio & State
    this.ws = null;
    this.audioCtx = null;
    this.outputGain = null;
    this.compressor = null;
    this.micStream = null;
    this.workletNode = null;
    this.isMuted = false;
    this.state = 'connecting'; // connecting | listening | speaking | thinking | ended

    // Playback state
    this.playbackQueue = [];
    this.activeSourceNodes = [];
    this.nextPlayTime = 0;
    this.isAiSpeaking = false;

    // Timer
    this.startTime = null;
    this.timerInterval = null;

    // Cache synthesized voices
    this.cachedVoices = [];
    if ('speechSynthesis' in window) {
      this.cachedVoices = window.speechSynthesis.getVoices();
      window.speechSynthesis.onvoiceschanged = () => {
        this.cachedVoices = window.speechSynthesis.getVoices();
      };
    }
  }

  async start() {
    try {
      this.setState('connecting');

      // Initialize Web Audio Context
      const AudioContextClass = window.AudioContext || window.webkitAudioContext;
      this.audioCtx = new AudioContextClass({ latencyHint: 'interactive' });
      if (this.audioCtx.state === 'suspended') {
        await this.audioCtx.resume();
      }

      // Master output amplifier & dynamics compressor for loud, clear phone loudspeaker playback
      this.outputGain = this.audioCtx.createGain();
      this.outputGain.gain.setValueAtTime(2.2, this.audioCtx.currentTime); // 2.2x volume boost for mobile speakers

      this.compressor = this.audioCtx.createDynamicsCompressor();
      this.compressor.threshold.setValueAtTime(-24, this.audioCtx.currentTime);
      this.compressor.knee.setValueAtTime(30, this.audioCtx.currentTime);
      this.compressor.ratio.setValueAtTime(12, this.audioCtx.currentTime);
      this.compressor.attack.setValueAtTime(0.003, this.audioCtx.currentTime);
      this.compressor.release.setValueAtTime(0.25, this.audioCtx.currentTime);

      this.outputGain.connect(this.compressor);
      this.compressor.connect(this.audioCtx.destination);

      // Request microphone access
      this.micStream = await navigator.mediaDevices.getUserMedia({
        audio: {
          channelCount: 1,
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
        },
      });

      // Load PCM downsampler AudioWorklet
      try {
        await this.audioCtx.audioWorklet.addModule('/js/pcm-processor.js');
        const sourceNode = this.audioCtx.createMediaStreamSource(this.micStream);
        this.workletNode = new AudioWorkletNode(this.audioCtx, 'pcm-processor');

        // Handle audio chunks from worklet
        this.workletNode.port.onmessage = (event) => {
          const { pcm, rms } = event.data;
          if (!pcm) return;

          // Suppress mic transmission while AI is speaking or muted
          // This completely cuts speaker bleed feedback to the microphone
          if (this.isAiSpeaking || this.isMuted) return;

          // Send 16kHz PCM audio chunk to WebSocket
          if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            const base64Audio = this.arrayBufferToBase64(pcm);
            this.ws.send(JSON.stringify({
              type: 'audio',
              data: base64Audio,
            }));
          }
        };

        sourceNode.connect(this.workletNode);
        const silentGain = this.audioCtx.createGain();
        silentGain.gain.value = 0;
        this.workletNode.connect(silentGain);
        silentGain.connect(this.audioCtx.destination);
      } catch (workletErr) {
        console.warn('[AudioCall] AudioWorklet init notice:', workletErr);
      }

      // Connect WebSocket
      this.connectWebSocket();

    } catch (err) {
      console.error('[AudioCall] Startup error:', err);
      this.onError('Microphone or Audio initialization failed: ' + (err.message || err));
      this.endCall();
    }
  }

  connectWebSocket() {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    let url = `${protocol}//${window.location.host}/ws/live?persona_id=${encodeURIComponent(this.personaId)}`;
    if (this.resumeId) {
      url += `&resume_id=${encodeURIComponent(this.resumeId)}`;
    }
    if (this.apiKey) {
      url += `&api_key=${encodeURIComponent(this.apiKey)}`;
    }

    this.ws = new WebSocket(url);

    this.ws.onopen = () => {
      console.log('[AudioCall] WebSocket connected to server proxy');
    };

    this.ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data);
        this.handleServerMessage(msg);
      } catch (e) {
        console.error('[AudioCall] Error parsing server message:', e);
      }
    };

    this.ws.onerror = (err) => {
      console.error('[AudioCall] WebSocket error:', err);
      this.onError('Connection error to voice server.');
    };

    this.ws.onclose = (event) => {
      console.log('[AudioCall] WebSocket closed', event.code, event.reason);
      if (this.state !== 'ended') {
        this.endCall();
      }
    };
  }

  handleServerMessage(msg) {
    switch (msg.type) {
      case 'connected':
        console.log(`[AudioCall] Session active with model: ${msg.model} (${msg.persona})`);
        this.setState('thinking');
        this.startCallTimer();
        break;

      case 'audio':
        if (msg.data) {
          this.enqueueAudioChunk(msg.data);
        }
        break;

      case 'speech':
        if (msg.text) {
          this.speakText(msg.text);
        }
        break;

      case 'interrupted':
        console.log('[AudioCall] Interrupt received - flushing audio');
        this.flushAudioQueue();
        this.setState('listening');
        break;

      case 'turn_complete':
        if (this.activeSourceNodes.length === 0) {
          this.setAiSpeaking(false);
          this.setState('listening');
        }
        break;

      case 'topic_saved':
        console.log('[AudioCall] Topic saved:', msg.topic);
        this.onTopicSaved(msg.topic);
        break;

      case 'error':
        console.error('[AudioCall] Server reported error:', msg.message);
        this.onError(msg.message);
        break;
    }
  }

  // Explicit manual interrupt triggered only by user button press
  manualInterrupt() {
    console.log('[AudioCall] Manual interrupt requested by user button');
    this.flushAudioQueue();
    this.setState('listening');

    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: 'client_interrupt' }));
    }
  }

  handleLocalBargeIn() {
    this.manualInterrupt();
  }

  // Speak text using Web Speech API synthesis
  speakText(rawText) {
    if (!('speechSynthesis' in window)) {
      console.warn('[AudioCall] SpeechSynthesis not supported in browser');
      return;
    }
    this.flushAudioQueue();

    // Clean text of markdown formatting (asterisks, hashtags, quotes) for clean spoken output
    const cleanText = rawText
      .replace(/#{1,6}\s*/g, '')
      .replace(/\*\*/g, '')
      .replace(/\*/g, '')
      .replace(/>/g, '')
      .replace(/_{1,2}/g, '')
      .replace(/`{1,3}/g, '')
      .trim();

    if (!cleanText) return;

    const utterance = new SpeechSynthesisUtterance(cleanText);
    utterance.lang = 'en-US';
    utterance.rate = 0.96; // calm, natural teacher pace
    utterance.pitch = 1.05;

    // Pick best natural English voice
    let voices = window.speechSynthesis.getVoices();
    if (!voices || voices.length === 0) {
      voices = this.cachedVoices || [];
    }
    const enVoice = voices.find(v => v.lang.startsWith('en') && (
      v.name.includes('Samantha') || 
      v.name.includes('Ava') || 
      v.name.includes('Allison') || 
      v.name.includes('Siri') || 
      v.name.includes('Serena') || 
      v.name.includes('Natural') || 
      v.name.includes('Google') || 
      v.name.includes('Jenny')
    )) || voices.find(v => v.lang === 'en-US') || voices.find(v => v.lang.startsWith('en'));
    if (enVoice) {
      utterance.voice = enVoice;
    }

    utterance.onstart = () => {
      this.setAiSpeaking(true);
    };

    utterance.onend = () => {
      this.setAiSpeaking(false);
    };

    utterance.onerror = (e) => {
      console.warn('[AudioCall] SpeechSynthesis notice:', e);
      this.setAiSpeaking(false);
    };

    window.speechSynthesis.speak(utterance);
  }

  // Enqueue 24kHz PCM chunk received from Gemini Live
  enqueueAudioChunk(base64Data) {
    if (!this.audioCtx || this.audioCtx.state === 'closed') return;

    try {
      const rawBytes = this.base64ToArrayBuffer(base64Data);
      const int16Array = new Int16Array(rawBytes);
      const float32Array = new Float32Array(int16Array.length);

      // Convert 16-bit PCM to normalized Float32 [-1.0, 1.0]
      for (let i = 0; i < int16Array.length; i++) {
        float32Array[i] = int16Array[i] / 32768.0;
      }

      // Gemini Live outputs 24kHz audio
      const sampleRate = 24000;
      const audioBuffer = this.audioCtx.createBuffer(1, float32Array.length, sampleRate);
      audioBuffer.getChannelData(0).set(float32Array);

      this.scheduleAudioPlayback(audioBuffer);
    } catch (e) {
      console.error('[AudioCall] Error decoding audio chunk:', e);
    }
  }

  scheduleAudioPlayback(buffer) {
    const now = this.audioCtx.currentTime;
    if (this.nextPlayTime < now) {
      this.nextPlayTime = now + 0.03;
    }

    const source = this.audioCtx.createBufferSource();
    source.buffer = buffer;
    
    // Connect to master output amplifier (2.2x gain + compressor)
    if (this.outputGain) {
      source.connect(this.outputGain);
    } else {
      source.connect(this.audioCtx.destination);
    }

    source.start(this.nextPlayTime);
    this.nextPlayTime += buffer.duration;

    this.activeSourceNodes.push(source);
    this.setAiSpeaking(true);

    source.onended = () => {
      const idx = this.activeSourceNodes.indexOf(source);
      if (idx !== -1) {
        this.activeSourceNodes.splice(idx, 1);
      }
      if (this.activeSourceNodes.length === 0) {
        this.setAiSpeaking(false);
      }
    };
  }

  flushAudioQueue() {
    if ('speechSynthesis' in window) {
      try {
        window.speechSynthesis.cancel();
      } catch (e) {}
    }

    for (const source of this.activeSourceNodes) {
      try {
        source.stop();
        source.disconnect();
      } catch (e) {}
    }
    this.activeSourceNodes = [];
    if (this.audioCtx) {
      this.nextPlayTime = this.audioCtx.currentTime;
    }
    this.setAiSpeaking(false);
  }

  setAiSpeaking(speaking) {
    if (this.thinkingTimer) {
      clearTimeout(this.thinkingTimer);
      this.thinkingTimer = null;
    }

    this.isAiSpeaking = speaking;
    if (speaking) {
      this.setState('speaking');
    } else {
      if (this.state === 'speaking' || this.state === 'thinking') {
        this.setState('listening');
      }
    }
  }

  setState(newState) {
    this.state = newState;
    this.onStateChange(newState);
  }

  passTurn() {
    if (this.isAiSpeaking) {
      // If AI is currently speaking, pressing this button triggers manual Interrupt!
      this.manualInterrupt();
      return;
    }

    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.setState('thinking');
      if (this.thinkingTimer) clearTimeout(this.thinkingTimer);
      this.thinkingTimer = setTimeout(() => {
        if (this.state === 'thinking') {
          console.warn('[AudioCall] Response timeout - switching back to listening');
          this.setState('listening');
        }
      }, 18000);
      this.ws.send(JSON.stringify({
        type: 'pass_turn',
        text: '',
      }));
    }
  }

  bookmarkTopic() {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: 'bookmark' }));
    }
  }

  toggleMute() {
    this.isMuted = !this.isMuted;
    if (this.workletNode) {
      this.workletNode.port.postMessage({ mute: this.isMuted });
    }
    return this.isMuted;
  }

  startCallTimer() {
    this.startTime = Date.now();
    this.updateTimer();
    this.timerInterval = setInterval(() => this.updateTimer(), 1000);
  }

  updateTimer() {
    if (!this.startTime) return;
    const elapsedSec = Math.floor((Date.now() - this.startTime) / 1000);
    const mins = Math.floor(elapsedSec / 60);
    const secs = elapsedSec % 60;
    const formatted = `${String(mins).padStart(2, '0')}:${String(secs).padStart(2, '0')}`;
    this.onTimerUpdate(formatted, elapsedSec);
  }

  endCall() {
    if (this.state === 'ended') return;
    this.setState('ended');

    if (this.timerInterval) {
      clearInterval(this.timerInterval);
      this.timerInterval = null;
    }

    this.flushAudioQueue();

    if (this.ws) {
      try {
        this.ws.close();
      } catch (e) {}
      this.ws = null;
    }

    if (this.micStream) {
      this.micStream.getTracks().forEach((track) => track.stop());
      this.micStream = null;
    }

    if (this.audioCtx && this.audioCtx.state !== 'closed') {
      try {
        this.audioCtx.close();
      } catch (e) {}
    }

    this.onCallEnded();
  }

  // Utilities
  arrayBufferToBase64(buffer) {
    let binary = '';
    const bytes = new Uint8Array(buffer);
    const len = bytes.byteLength;
    for (let i = 0; i < len; i++) {
      binary += String.fromCharCode(bytes[i]);
    }
    return window.btoa(binary);
  }

  base64ToArrayBuffer(base64) {
    const binaryString = window.atob(base64);
    const len = binaryString.length;
    const bytes = new Uint8Array(len);
    for (let i = 0; i < len; i++) {
      bytes[i] = binaryString.charCodeAt(i);
    }
    return bytes.buffer;
  }
}

window.AudioCallManager = AudioCallManager;
