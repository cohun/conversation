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

  isMobileDevice() {
    return /Android|webOS|iPhone|iPad|iPod|BlackBerry|IEMobile|Opera Mini/i.test(navigator.userAgent) ||
      (navigator.userAgent.includes('Macintosh') && navigator.maxTouchPoints > 1);
  }

  applyAudioSessionMode() {
    if (!this.isMobileDevice()) return;
    if ('audioSession' in navigator) {
      try {
        if (this.state === 'listening' || !this.isSpeakerphone) {
          navigator.audioSession.type = 'play-and-record';
        } else {
          // In 'speaking' / 'thinking' with speakerphone: pure media playback routes to loudspeaker!
          navigator.audioSession.type = 'playback';
        }
      } catch (e) {
        console.warn('[AudioCall] navigator.audioSession notice:', e);
      }
    }
  }

  applyAudioRouting() {
    if (!this.outputGain || !this.audioCtx) return;

    const isMobile = this.isMobileDevice();
    const targetGain = (isMobile && this.isSpeakerphone) ? 4.2 : (isMobile ? 1.2 : 1.15);
    this.outputGain.gain.setValueAtTime(targetGain, this.audioCtx.currentTime);

    console.log(`[AudioCall] 🔊 Audio route: CegAgy Chain (Compressor -> Gain ${targetGain}x -> Destination) [${isMobile ? (this.isSpeakerphone ? 'Mobile Loudspeaker' : 'Mobile Earpiece') : 'Desktop 1.15x'}]`);
  }

  toggleSpeakerphone() {
    this.isSpeakerphone = !this.isSpeakerphone;
    this.applyAudioRouting();
    if (this.isMobileDevice()) {
      if (this.isSpeakerphone && this.state !== 'listening') {
        this.stopMicrophoneCapture();
      } else if (!this.isSpeakerphone && !this.micStream) {
        this.startMicrophoneCapture();
      }
      this.applyAudioSessionMode();
    }
    return this.isSpeakerphone;
  }

  setSpeakerphone(enabled) {
    this.isSpeakerphone = !!enabled;
    this.applyAudioRouting();
    if (this.isMobileDevice()) {
      this.applyAudioSessionMode();
    }
    return this.isSpeakerphone;
  }

  async startMicrophoneCapture() {
    if (this.micStream) return;
    try {
      this.applyAudioSessionMode();
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
    this.applyAudioSessionMode();
  }

  async start() {
    try {
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
          await this.audioCtx.audioWorklet.addModule('/js/pcm-processor.js?v=2.4.7');
          this.workletModuleLoaded = true;
        } catch (e) {
          this.workletModuleLoaded = false;
        }
      }

      // 3. Audio Graph Setup: Source -> Compressor -> Gain -> Destination (CegAgy Architecture)
      // Desktop: 1.15x gain (clean, comfortable listening volume for headphones/speakers)
      // Mobile: 4.2x gain + Dynamics Compressor (CegAgy hardware tuned for phone loudspeakers)
      const targetGain = (isMobile && this.isSpeakerphone) ? 4.2 : (isMobile ? 1.2 : 1.15);
      this.outputGain = this.audioCtx.createGain();
      this.outputGain.gain.setValueAtTime(targetGain, this.audioCtx.currentTime);

      this.compressor = this.audioCtx.createDynamicsCompressor();
      if (isMobile) {
        this.compressor.threshold.setValueAtTime(-28, this.audioCtx.currentTime);
        this.compressor.knee.setValueAtTime(35, this.audioCtx.currentTime);
        this.compressor.ratio.setValueAtTime(16, this.audioCtx.currentTime);
        this.compressor.attack.setValueAtTime(0.002, this.audioCtx.currentTime);
        this.compressor.release.setValueAtTime(0.20, this.audioCtx.currentTime);
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

      if (isMobile) {
        // Mobile: Immediate Hardware Teardown! Sarah speaks first!
        // Releasing microphone tracks immediately returns phone to pure media stereo loudspeaker mode!
        permStream.getTracks().forEach((track) => track.stop());
        this.micStream = null;
        this.applyAudioSessionMode();
        console.log('[AudioCall] 🔊 Mobile initial Hardware Teardown: mic released for Sarah greeting');
      } else {
        // Desktop: Maintain persistent mic stream as verified and working
        this.micStream = permStream;
        await this.initMicrophoneCapture();
      }

      // 5. Connect WebSocket to server proxy
      this.connectWebSocket();

    } catch (err) {
      console.error('[AudioCall] Startup error:', err);
      this.onError('Mikrofon vagy hang inicializálási hiba: ' + (err.message || err));
      this.endCall();
    }
  }

  async initMicrophoneCapture() {
    if (!this.micStream) return;
    let workletInitialized = false;

    // Attempt 1: Modern AudioWorklet
    if (this.audioCtx.audioWorklet) {
      try {
        if (!this.workletModuleLoaded) {
          await this.audioCtx.audioWorklet.addModule('/js/pcm-processor.js?v=2.4.7');
          this.workletModuleLoaded = true;
        }
        this.micSourceNode = this.audioCtx.createMediaStreamSource(this.micStream);
        this.workletNode = new AudioWorkletNode(this.audioCtx, 'pcm-processor');

        this.workletNode.port.onmessage = (event) => {
          const { pcm, rms } = event.data;
          this.handleMicPcmChunk(pcm, rms);
        };

        this.micSourceNode.connect(this.workletNode);
        this.silentGainNode = this.audioCtx.createGain();
        this.silentGainNode.gain.value = 0;
        this.workletNode.connect(this.silentGainNode);
        this.silentGainNode.connect(this.audioCtx.destination);
        workletInitialized = true;
        console.log('[AudioCall] 🎙️ AudioWorklet microphone capture initialized');
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

      this.micSourceNode.connect(scriptNode);
      this.silentGainNode = this.audioCtx.createGain();
      this.silentGainNode.gain.value = 0;
      scriptNode.connect(this.silentGainNode);
      this.silentGainNode.connect(this.audioCtx.destination);
      this.scriptProcessorNode = scriptNode;
      console.log('[AudioCall] 🎙️ ScriptProcessor fallback microphone capture active');
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
      const now = Date.now();
      if (!this.lastMicLogTime || now - this.lastMicLogTime > 2000) {
        console.log(`[AudioCall] 🎙️ Mic streaming: ${this.sentAudioChunkCount} chunks streamed (RMS: ${rms.toFixed(3)})`);
        this.lastMicLogTime = now;
      }

      // Visual feedback callback for orb pulsation
      if (this.onAudioActivity) {
        this.onAudioActivity(rms);
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

    this.isAiSpeaking = speaking;
    if (speaking) {
      this.setState('speaking');
    }
  }

  async setState(newState) {
    this.state = newState;
    const isMobile = this.isMobileDevice();

    if (isMobile && this.isSpeakerphone) {
      if (newState === 'listening') {
        // Diák köre: mikrofon indítása
        await this.startMicrophoneCapture();
      } else if (newState === 'thinking' || newState === 'speaking' || newState === 'connecting') {
        // Sarah köre: Hardware Teardown (t.stop()) -> telefon azonnal visszatér tiszta sztereó média-hangszóró módba!
        this.stopMicrophoneCapture();
      }
    }

    this.onStateChange(newState);
  }

  passTurn() {
    // Check if AI is genuinely speaking with active audio nodes in queue
    const isActuallyPlaying = this.isAiSpeaking && this.activeSourceNodes.length > 0;
    if (isActuallyPlaying) {
      this.manualInterrupt();
      return;
    }

    this.turnCompleteReceived = false;
    this.setAiSpeaking(false);

    // Mobilon: Hardware Teardown azonnal a gomb megnyomásakor
    if (this.isMobileDevice() && this.isSpeakerphone) {
      this.stopMicrophoneCapture();
    }

    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      console.log(`[AudioCall] 🎯 passTurn: passing turn to partner (chunks streamed in turn: ${this.sentAudioChunkCount})`);
      this.sentAudioChunkCount = 0;

      this.setState('thinking');
      if (this.thinkingTimer) clearTimeout(this.thinkingTimer);
      this.thinkingTimer = setTimeout(() => {
        if (this.state === 'thinking') {
          console.warn('[AudioCall] Response timeout - switching back to listening');
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
    console.log('[AudioCall] Manual interrupt requested by user button');
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
