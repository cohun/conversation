// Antigravity Voice Call Manager
// Handles Web Audio API, AudioWorklet (16kHz PCM), ScriptProcessor Fallback, 24kHz PCM Playback, Speech Synthesis, WebSocket, and Turn-Taking

class AudioCallManager {
  constructor(options = {}) {
    this.personaId = options.personaId || 'teacher-sarah';
    this.resumeId = options.resumeId || '';
    this.apiKey = options.apiKey || '';

    // Callbacks
    this.onStateChange = options.onStateChange || (() => {});
    this.onTimerUpdate = options.onTimerUpdate || (() => {});
    this.onAudioActivity = options.onAudioActivity || (() => {});
    this.onTopicSaved = options.onTopicSaved || (() => {});
    this.onError = options.onError || (() => {});
    this.onCallEnded = options.onCallEnded || (() => {});

    // Audio & State
    this.ws = null;
    this.audioCtx = null;
    this.outputGain = null;
    this.compressor = null;
    this.isSpeakerphone = true; // Default to loudspeaker on mobile devices
    this.micStream = null;
    this.micSourceNode = null;
    this.micPreAmpGain = null;
    this.micCompressor = null;
    this.workletNode = null;
    this.scriptProcessorNode = null;
    this.silentGainNode = null;
    this.workletModuleLoaded = false;
    this.isMuted = false;
    this.state = 'connecting'; // connecting | listening | speaking | thinking | ended

    // Playback state
    this.activeSourceNodes = [];
    this.nextPlayTime = 0;
    this.isAiSpeaking = false;
    this.turnCompleteReceived = false;
    this.turnCompletionTimer = null;
    this.thinkingTimer = null;

    // Mic statistics
    this.sentAudioChunkCount = 0;
    this.lastMicLogTime = 0;
    this.peakRmsSinceLog = 0.0;

    // Timer
    this.startTime = null;
    this.timerInterval = null;

    // Natural VAD Turn-Taking & Waiting Chime Heartbeat
    this.consecutiveSpeechChunks = 0;
    this.consecutiveSilenceChunks = 0;
    this.userHasSpokenInTurn = false;
    this.waitingChimeInterval = null;

    // Cache synthesized voices
    this.cachedVoices = [];
    if ('speechSynthesis' in window) {
      this.cachedVoices = window.speechSynthesis.getVoices();
      window.speechSynthesis.onvoiceschanged = () => {
        this.cachedVoices = window.speechSynthesis.getVoices();
      };
    }
  }

  isMobileDevice() {
    return /Android|webOS|iPhone|iPad|iPod|BlackBerry|IEMobile|Opera Mini/i.test(navigator.userAgent) ||
      (navigator.userAgent.includes('Macintosh') && navigator.maxTouchPoints > 1);
  }

  applyAudioRouting() {
    if (!this.outputGain || !this.audioCtx) return;

    const isMobile = this.isMobileDevice();
    // Mobile Loudspeaker / Car Bluetooth: 2.3x (clean, distortion-free headroom)
    // Mobile Earpiece: 1.2x
    // Desktop: 1.15x
    const targetGain = (isMobile && this.isSpeakerphone) ? 2.3 : (isMobile ? 1.2 : 1.15);
    this.outputGain.gain.setValueAtTime(targetGain, this.audioCtx.currentTime);

    console.log(`[AudioCall] 🔊 Audio route: CegAgy Chain (Compressor -> Gain ${targetGain}x -> Destination) [${isMobile ? (this.isSpeakerphone ? 'Mobile Loudspeaker (Car-tuned 2.3x)' : 'Mobile Earpiece') : 'Desktop 1.15x'}]`);
  }

  toggleSpeakerphone() {
    this.isSpeakerphone = !this.isSpeakerphone;
    this.applyAudioRouting();
    return this.isSpeakerphone;
  }

  setSpeakerphone(enabled) {
    this.isSpeakerphone = !!enabled;
    this.applyAudioRouting();
    return this.isSpeakerphone;
  }

  async startMicrophoneCapture() {
    if (this.micStream) return;
    try {
      this.micStream = await navigator.mediaDevices.getUserMedia({
        audio: {
          channelCount: 1,
          echoCancellation: true,
          noiseSuppression: false,
          autoGainControl: true,
        },
      });
      await this.initMicrophoneCapture();
      console.log('[AudioCall] 🎙️ Microphone capture active');
    } catch (err) {
      console.error('[AudioCall] startMicrophoneCapture error:', err);
    }
  }

  stopMicrophoneCapture() {
    if (this.workletNode) {
      try {
        this.workletNode.port.onmessage = null;
        this.workletNode.disconnect();
      } catch (e) {}
      this.workletNode = null;
    }
    if (this.scriptProcessorNode) {
      try {
        this.scriptProcessorNode.onaudioprocess = null;
        this.scriptProcessorNode.disconnect();
      } catch (e) {}
      this.scriptProcessorNode = null;
    }
    if (this.silentGainNode) {
      try {
        this.silentGainNode.disconnect();
      } catch (e) {}
      this.silentGainNode = null;
    }
    if (this.micCompressor) {
      try {
        this.micCompressor.disconnect();
      } catch (e) {}
      this.micCompressor = null;
    }
    if (this.micPreAmpGain) {
      try {
        this.micPreAmpGain.disconnect();
      } catch (e) {}
      this.micPreAmpGain = null;
    }
    if (this.micSourceNode) {
      try {
        this.micSourceNode.disconnect();
      } catch (e) {}
      this.micSourceNode = null;
    }
    if (this.micStream) {
      try {
        this.micStream.getTracks().forEach((track) => track.stop());
      } catch (e) {}
      this.micStream = null;
      console.log('[AudioCall] 🛑 Mobile Hardware Teardown: mic stopped, phone released to media loudspeaker mode!');
    }
  }

  async start() {
    try {
      if ('audioSession' in navigator) {
        try {
          navigator.audioSession.type = 'auto';
        } catch (e) {}
      }

      this.setState('connecting');
      const isMobile = this.isMobileDevice();

      // 1. Initialize Web Audio Context (request interactive latency)
      const AudioContextClass = window.AudioContext || window.webkitAudioContext;
      this.audioCtx = new AudioContextClass({ latencyHint: 'interactive' });
      if (this.audioCtx.state === 'suspended') {
        await this.audioCtx.resume();
      }

      // 2. Pre-load AudioWorklet module
      if (this.audioCtx.audioWorklet) {
        try {
          await this.audioCtx.audioWorklet.addModule('/js/pcm-processor.js?v=2.4.22');
          this.workletModuleLoaded = true;
        } catch (e) {
          this.workletModuleLoaded = false;
        }
      }

      // 3. Audio Graph Setup: Source -> Compressor -> Gain -> Destination (CegAgy Architecture)
      // Desktop: 1.15x gain (clean, comfortable listening volume for headphones/speakers)
      // Mobile: 2.3x gain + Dynamics Compressor (tuned for clean car audio & phone loudspeakers without clipping)
      const targetGain = (isMobile && this.isSpeakerphone) ? 2.3 : (isMobile ? 1.2 : 1.15);
      this.outputGain = this.audioCtx.createGain();
      this.outputGain.gain.setValueAtTime(targetGain, this.audioCtx.currentTime);

      this.compressor = this.audioCtx.createDynamicsCompressor();
      if (isMobile) {
        // Tuned for automobile speakers & phone loudspeaker: gentle, broadcast-quality compression without harsh clipping
        this.compressor.threshold.setValueAtTime(-20, this.audioCtx.currentTime);
        this.compressor.knee.setValueAtTime(25, this.audioCtx.currentTime);
        this.compressor.ratio.setValueAtTime(4, this.audioCtx.currentTime);
        this.compressor.attack.setValueAtTime(0.005, this.audioCtx.currentTime);
        this.compressor.release.setValueAtTime(0.15, this.audioCtx.currentTime);
      } else {
        this.compressor.threshold.setValueAtTime(-10, this.audioCtx.currentTime);
        this.compressor.knee.setValueAtTime(15, this.audioCtx.currentTime);
        this.compressor.ratio.setValueAtTime(3, this.audioCtx.currentTime);
        this.compressor.attack.setValueAtTime(0.01, this.audioCtx.currentTime);
        this.compressor.release.setValueAtTime(0.15, this.audioCtx.currentTime);
      }

      // Compressor connects to OutputGain, OutputGain connects to Destination!
      this.compressor.connect(this.outputGain);
      this.outputGain.connect(this.audioCtx.destination);

      // 4. Request initial microphone permission within the user gesture
      const permStream = await navigator.mediaDevices.getUserMedia({
        audio: {
          channelCount: 1,
          echoCancellation: true,
          noiseSuppression: false,
          autoGainControl: true,
        },
      });

      // Maintain persistent mic stream across all devices (no mid-call hardware teardown)
      this.micStream = permStream;
      await this.initMicrophoneCapture();

      // 5. Connect WebSocket to server proxy
      this.connectWebSocket();

    } catch (err) {
      console.error('[AudioCall] Startup error:', err);
      this.onError('Mikrofon vagy hang inicializálási hiba: ' + (err.message || err));
      this.endCall();
    }
  }

  setupMicPreAmpChain(sourceNode) {
    this.micPreAmpGain = this.audioCtx.createGain();
    this.micPreAmpGain.gain.setValueAtTime(3.0, this.audioCtx.currentTime);

    this.micCompressor = this.audioCtx.createDynamicsCompressor();
    this.micCompressor.threshold.setValueAtTime(-12, this.audioCtx.currentTime);
    this.micCompressor.knee.setValueAtTime(18, this.audioCtx.currentTime);
    this.micCompressor.ratio.setValueAtTime(4, this.audioCtx.currentTime);
    this.micCompressor.attack.setValueAtTime(0.003, this.audioCtx.currentTime);
    this.micCompressor.release.setValueAtTime(0.15, this.audioCtx.currentTime);

    sourceNode.connect(this.micPreAmpGain);
    this.micPreAmpGain.connect(this.micCompressor);
    return this.micCompressor;
  }

  async initMicrophoneCapture() {
    if (!this.micStream) return;
    let workletInitialized = false;

    // Attempt 1: Modern AudioWorklet
    if (this.audioCtx.audioWorklet) {
      try {
        if (!this.workletModuleLoaded) {
          await this.audioCtx.audioWorklet.addModule('/js/pcm-processor.js?v=2.4.20');
          this.workletModuleLoaded = true;
        }
        this.micSourceNode = this.audioCtx.createMediaStreamSource(this.micStream);
        const preAmpOut = this.setupMicPreAmpChain(this.micSourceNode);
        this.workletNode = new AudioWorkletNode(this.audioCtx, 'pcm-processor');

        this.workletNode.port.onmessage = (event) => {
          const { pcm, rms } = event.data;
          this.handleMicPcmChunk(pcm, rms);
        };

        preAmpOut.connect(this.workletNode);
        this.silentGainNode = this.audioCtx.createGain();
        this.silentGainNode.gain.value = 0;
        this.workletNode.connect(this.silentGainNode);
        this.silentGainNode.connect(this.audioCtx.destination);
        workletInitialized = true;
        console.log('[AudioCall] 🎙️ AudioWorklet microphone capture initialized (with 3.0x studio pre-amp)');
      } catch (workletErr) {
        console.warn('[AudioCall] AudioWorklet init failed, using ScriptProcessor fallback:', workletErr);
      }
    }

    // Attempt 2: Bulletproof ScriptProcessor Fallback (guaranteed to work across all mobile/desktop browsers)
    if (!workletInitialized) {
      this.initScriptProcessorFallback();
    }
  }

  initScriptProcessorFallback() {
    if (!this.micStream) return;
    try {
      const bufferSize = 2048;
      this.micSourceNode = this.audioCtx.createMediaStreamSource(this.micStream);
      const preAmpOut = this.setupMicPreAmpChain(this.micSourceNode);
      const scriptNode = this.audioCtx.createScriptProcessor(bufferSize, 1, 1);

      const targetSampleRate = 16000;
      const chunkSize = 1600; // 100ms at 16kHz
      const pcmBuffer = new Int16Array(chunkSize);
      let pcmIndex = 0;
      let spPhase = 0.0;
      let spChunkSumSquares = 0.0;

      scriptNode.onaudioprocess = (e) => {
        if (this.isMuted) return;
        const inputData = e.inputBuffer.getChannelData(0);
        const inputSampleRate = this.audioCtx.sampleRate;

        // Fast path: direct copy if native 16kHz
        if (inputSampleRate === targetSampleRate) {
          for (let i = 0; i < inputData.length; i++) {
            let s = Math.max(-1, Math.min(1, inputData[i]));
            spChunkSumSquares += s * s;
            const sample16 = s < 0 ? s * 0x8000 : s * 0x7FFF;
            pcmBuffer[pcmIndex++] = sample16;

            if (pcmIndex >= chunkSize) {
              const chunkRms = Math.sqrt(spChunkSumSquares / chunkSize);
              spChunkSumSquares = 0.0;
              const chunkToSend = new Int16Array(pcmBuffer);
              this.handleMicPcmChunk(chunkToSend.buffer, chunkRms);
              pcmIndex = 0;
            }
          }
          return;
        }

        // High fidelity continuous linear interpolation resampler
        const ratio = inputSampleRate / targetSampleRate;
        while (spPhase < inputData.length) {
          const i0 = Math.floor(spPhase);
          const i1 = Math.min(inputData.length - 1, i0 + 1);
          const frac = spPhase - i0;
          const s = inputData[i0] + frac * (inputData[i1] - inputData[i0]);

          const clamped = Math.max(-1, Math.min(1, s));
          spChunkSumSquares += clamped * clamped;
          const sample16 = clamped < 0 ? clamped * 0x8000 : clamped * 0x7FFF;
          pcmBuffer[pcmIndex++] = sample16;

          if (pcmIndex >= chunkSize) {
            const chunkRms = Math.sqrt(spChunkSumSquares / chunkSize);
            spChunkSumSquares = 0.0;
            const chunkToSend = new Int16Array(pcmBuffer);
            this.handleMicPcmChunk(chunkToSend.buffer, chunkRms);
            pcmIndex = 0;
          }

          spPhase += ratio;
        }
        spPhase -= inputData.length;
      };

      preAmpOut.connect(scriptNode);
      this.silentGainNode = this.audioCtx.createGain();
      this.silentGainNode.gain.value = 0;
      scriptNode.connect(this.silentGainNode);
      this.silentGainNode.connect(this.audioCtx.destination);
      this.scriptProcessorNode = scriptNode;
      console.log('[AudioCall] 🎙️ ScriptProcessor fallback microphone capture active (with 3.0x studio pre-amp)');
    } catch (spErr) {
      console.error('[AudioCall] Failed to initialize ScriptProcessor fallback:', spErr);
    }
  }

  handleMicPcmChunk(pcm, rms) {
    if (!pcm) return;
    if (this.isMuted) return;

    // Strict State Guard: ONLY stream microphone audio when state is 'listening'
    // Absolutely NEVER stream while 'thinking', 'speaking', or while AI is speaking
    if (this.state !== 'listening' || this.isAiSpeaking) {
      return;
    }

    // Send 16kHz PCM audio chunk to WebSocket
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      const base64Audio = this.arrayBufferToBase64(pcm);
      this.ws.send(JSON.stringify({
        type: 'audio',
        data: base64Audio,
      }));

      this.sentAudioChunkCount++;
      if (rms > this.peakRmsSinceLog) {
        this.peakRmsSinceLog = rms;
      }
      const now = Date.now();
      if (!this.lastMicLogTime || now - this.lastMicLogTime > 2000) {
        console.log(`[AudioCall] 🎙️ Mic streaming: ${this.sentAudioChunkCount} chunks streamed (RMS: ${rms.toFixed(3)}, Peak: ${this.peakRmsSinceLog.toFixed(3)})`);
        this.lastMicLogTime = now;
        this.peakRmsSinceLog = 0.0;
      }

      // Visual feedback callback for orb pulsation
      if (this.onAudioActivity) {
        this.onAudioActivity(rms);
      }

      // Track speech activity for UI state without cutting the student off during thinking pauses!
      // Turn handover happens ONLY when the student says "Your turn!" (detected by server) or taps "Átadom a szót".
      if (rms > 0.010) {
        this.consecutiveSpeechChunks++;
        this.consecutiveSilenceChunks = 0;
        if (this.consecutiveSpeechChunks >= 3) {
          this.userHasSpokenInTurn = true;
        }
      } else if (rms < 0.005) {
        this.consecutiveSpeechChunks = 0;
        this.consecutiveSilenceChunks++;
      }
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
      this.onError('Kapcsolati hiba a hangkiszolgálóhoz.');
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
        console.log('[AudioCall] 🏁 turn_complete received from server');
        this.turnCompleteReceived = true;
        this.checkIfAiSpeechFinished();
        break;

      case 'turn_passed':
        console.log('[AudioCall] 🎯 turn_passed received from server (voice trigger or explicit pass)');
        this.sentAudioChunkCount = 0;
        this.userHasSpokenInTurn = false;
        this.consecutiveSpeechChunks = 0;
        this.consecutiveSilenceChunks = 0;
        if (!this.waitingChimeInterval) {
          this.startWaitingChimeHeartbeat();
        }
        this.setState('thinking');
        if (this.thinkingTimer) clearTimeout(this.thinkingTimer);
        this.thinkingTimer = setTimeout(() => {
          if (this.state === 'thinking') {
            console.warn('[AudioCall] Response timeout - switching back to listening');
            this.stopWaitingChimeHeartbeat();
            this.setState('listening');
          }
        }, 15000);
        break;

      case 'user_transcript':
        if (msg.text) {
          console.log('[AudioCall] 🗣️ User speech recognized by server:', msg.text);
          if (this.onUserTranscript) {
            this.onUserTranscript(msg.text);
          }
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

  // Explicit manual interrupt triggered only when user taps while partner is speaking
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

  // Speak text using Web Speech API synthesis (fallback)
  speakText(rawText) {
    this.stopWaitingChimeHeartbeat();
    if (!('speechSynthesis' in window)) {
      console.warn('[AudioCall] SpeechSynthesis not supported in browser');
      return;
    }
    this.flushAudioQueue();

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
    utterance.rate = 0.96;
    utterance.pitch = 1.05;

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
    this.stopWaitingChimeHeartbeat();
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
    if (!this.audioCtx || this.audioCtx.state === 'closed') return;
    if (this.audioCtx.state === 'suspended') {
      try {
        this.audioCtx.resume();
      } catch (e) {}
    }

    const now = this.audioCtx.currentTime;
    if (this.nextPlayTime < now) {
      this.nextPlayTime = now + 0.03;
    }

    const source = this.audioCtx.createBufferSource();
    source.buffer = buffer;

    // CegAgy Audio Chain: Source -> Compressor -> OutputGain -> Destination
    if (this.compressor) {
      source.connect(this.compressor);
    } else if (this.outputGain) {
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
      // Check for clean turn completion if server has marked generation complete
      if (this.turnCompleteReceived) {
        this.checkIfAiSpeechFinished();
      }
    };
  }

  checkIfAiSpeechFinished() {
    if (!this.turnCompleteReceived) return;

    if (!this.audioCtx) {
      this.finishAiTurn();
      return;
    }

    const now = this.audioCtx.currentTime;
    const remainingSec = this.nextPlayTime - now;

    if (this.turnCompletionTimer) {
      clearTimeout(this.turnCompletionTimer);
      this.turnCompletionTimer = null;
    }

    if (remainingSec <= 0.05 && this.activeSourceNodes.length === 0) {
      this.finishAiTurn();
    } else {
      // Schedule check when all queued audio has finished playing + 400ms safety cushion (CegAgy standard)
      const delayMs = Math.max(80, Math.ceil((remainingSec + 0.4) * 1000));
      this.turnCompletionTimer = setTimeout(() => {
        this.checkIfAiSpeechFinished();
      }, delayMs);
    }
  }

  async finishAiTurn() {
    if (this.turnCompletionTimer) {
      clearTimeout(this.turnCompletionTimer);
      this.turnCompletionTimer = null;
    }
    this.turnCompleteReceived = false;
    this.activeSourceNodes = [];
    this.setAiSpeaking(false);
    await this.setState('listening');
    console.log('[AudioCall] 👂 AI speech finished playing completely. Listening to student!');
  }

  flushAudioQueue() {
    this.stopWaitingChimeHeartbeat();
    if (this.turnCompletionTimer) {
      clearTimeout(this.turnCompletionTimer);
      this.turnCompletionTimer = null;
    }
    this.turnCompleteReceived = false;

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

    const wasSpeaking = this.isAiSpeaking;
    this.isAiSpeaking = speaking;
    if (speaking) {
      if (!wasSpeaking) {
        console.log('[AudioCall] 🔊 Sarah started speaking...');
      }
      this.aiSpeechStartTime = Date.now();
      this.stopWaitingChimeHeartbeat();
      this.setState('speaking');
    }
  }

  async setState(newState) {
    this.state = newState;

    if (newState === 'listening') {
      this.userHasSpokenInTurn = false;
      this.consecutiveSpeechChunks = 0;
      this.consecutiveSilenceChunks = 0;
    } else {
      if (newState !== 'thinking') {
        this.stopWaitingChimeHeartbeat();
      }
    }

    this.onStateChange(newState);
  }

  // Play discrete, gentle dual-tone waiting chime (528Hz + 660Hz)
  playWaitingChime() {
    if (!this.audioCtx || this.audioCtx.state === 'closed') return;
    if (this.audioCtx.state === 'suspended') {
      try {
        this.audioCtx.resume();
      } catch (e) {}
    }

    try {
      const now = this.audioCtx.currentTime;

      // Soft envelope to avoid clicks and provide gentle acoustic decay
      const chimeGain = this.audioCtx.createGain();
      chimeGain.gain.setValueAtTime(0.0001, now);
      // Gentle 25ms attack ramp
      chimeGain.gain.exponentialRampToValueAtTime(0.20, now + 0.025);
      // Smooth exponential decay over 650ms
      chimeGain.gain.exponentialRampToValueAtTime(0.0001, now + 0.65);

      // Dual harmonic pure sine waves: 528Hz (warm bell tone) + 660Hz (pure major third harmonic)
      const osc1 = this.audioCtx.createOscillator();
      osc1.type = 'sine';
      osc1.frequency.setValueAtTime(528, now);

      const osc2 = this.audioCtx.createOscillator();
      osc2.type = 'sine';
      osc2.frequency.setValueAtTime(660, now);

      osc1.connect(chimeGain);
      osc2.connect(chimeGain);
      chimeGain.connect(this.audioCtx.destination);

      osc1.start(now);
      osc2.start(now);
      osc1.stop(now + 0.70);
      osc2.stop(now + 0.70);
      console.log('[AudioCall] 🔔 Discrete waiting chime played');
    } catch (e) {
      console.warn('[AudioCall] Error playing waiting chime:', e);
    }
  }

  startWaitingChimeHeartbeat() {
    this.stopWaitingChimeHeartbeat();
    // Play immediately upon turn passing for instant driver reassurance
    this.playWaitingChime();

    // Repeat every 5 seconds while in thinking state until AI starts speaking
    this.waitingChimeInterval = setInterval(() => {
      if (this.state === 'thinking' && !this.isAiSpeaking) {
        this.playWaitingChime();
      } else {
        this.stopWaitingChimeHeartbeat();
      }
    }, 5000);
  }

  stopWaitingChimeHeartbeat() {
    if (this.waitingChimeInterval) {
      clearInterval(this.waitingChimeInterval);
      this.waitingChimeInterval = null;
    }
  }

  // Hands-Free Natural Voice Detection (Auto-VAD via PCM RMS)
  initVoiceRecognition() {
    // Note: webkitSpeechRecognition is intentionally not initialized here.
    // Chromium's Web Speech API forces exclusive CoreAudio/WASAPI capture which
    // mutes MediaStream / AudioWorklet to RMS 0.000. Realtime 16kHz audio is preserved!
  }

  startVoiceRecognition() {}
  stopVoiceRecognition() {}

  passTurn() {
    // Strict Guard: ONLY pass turn when in 'listening' state and AI is not speaking
    // Absolutely NEVER pass turn while already thinking or while AI is speaking!
    if (this.state !== 'listening' || this.isAiSpeaking) {
      console.log(`[AudioCall] passTurn ignored: state=${this.state}, isAiSpeaking=${this.isAiSpeaking}`);
      return;
    }

    this.turnCompleteReceived = false;
    this.userHasSpokenInTurn = false;
    this.consecutiveSpeechChunks = 0;
    this.consecutiveSilenceChunks = 0;

    // Play immediate feedback chime and start 5-second waiting heartbeat
    this.startWaitingChimeHeartbeat();

    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      console.log(`[AudioCall] 🎯 passTurn: passing turn to partner (chunks streamed in turn: ${this.sentAudioChunkCount})`);
      this.sentAudioChunkCount = 0;

      this.setState('thinking');
      if (this.thinkingTimer) clearTimeout(this.thinkingTimer);
      this.thinkingTimer = setTimeout(() => {
        if (this.state === 'thinking') {
          console.warn('[AudioCall] Response timeout - switching back to listening');
          this.stopWaitingChimeHeartbeat();
          this.setState('listening');
        }
      }, 15000);

      this.ws.send(JSON.stringify({
        type: 'pass_turn',
        text: '',
      }));
    }
  }

  async manualInterrupt() {
    // Barge-in Grace Period (1.5 seconds):
    // Prevent accidental taps right when AI starts speaking from wiping out Sarah's response!
    const elapsed = Date.now() - (this.aiSpeechStartTime || 0);
    if (this.isAiSpeaking && elapsed < 1500) {
      console.log(`[AudioCall] Manual interrupt ignored: only ${elapsed}ms into speech (1.5s grace period active)`);
      return;
    }

    console.log('[AudioCall] Manual interrupt confirmed by user');
    this.stopWaitingChimeHeartbeat();
    this.flushAudioQueue();
    await this.setState('listening');

    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: 'client_interrupt' }));
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
    this.state = 'ended';

    this.stopWaitingChimeHeartbeat();
    this.stopVoiceRecognition();

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

    this.stopMicrophoneCapture();

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
