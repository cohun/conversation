# Project Rules & Architectural Guardrails (English Teacher App — Sarah)

Whenever modifying the voice call pipeline (`internal/ws/session.go`, `internal/ws/hub.go`, `internal/gemini/`, or `web/static/js/audio-call.js`), you MUST strictly preserve the following three verified architectural invariants:

## 1. Gemini Live (`models/gemini-3.8-live`) Audio Comprehension Protocol
- **Root Cause**: The `models/gemini-3.8-live` bidirectional endpoint ignores user speech when sent solely via `RealtimeInput.MediaChunks` (`audio/pcm;rate=16000`) or when raw PCM is sent inside `ClientContent` without an accompanying `Text` part (`AudioChunks=0` received).
- **Mandatory Guardrail**:
  - In [`internal/ws/session.go`](internal/ws/session.go) (`sendBufferedAudioTurn`), always normalize the user's 16kHz mono PCM speech (`normalizePCM16`), wrap it in a 44-byte RIFF WAV header (`encodePCM16ToWAV`, `MimeType: "audio/wav"`), and send it inside `ClientContent` **paired with a `Text` prompt part** (`"Please respond naturally to what I just said in the attached audio."`) and `TurnComplete: true`.
  - Never replace the `ClientContent` (`audio/wav` + `Text`) turn payload with `RealtimeInput` PCM bursts or `RealtimeInput.ActivityEnd`.

## 2. Pause-Safe Turn-Taking & `"Your turn!"` Voice Trigger Detection
- **Root Cause**:
  1. Learners need arbitrarily long thinking pauses mid-thought without being interrupted by Sarah.
  2. Attaching a function tool (`trigger_turn_handover`) to the helper `BidiGenerateContent` session causes `models/gemini-3.8-live` to hallucinate tool calls even when no trigger phrase was spoken.
  3. If a background trigger check completes after the learner has already resumed speaking, calling `endUserTurn` cuts off the learner mid-sentence.
- **Mandatory Guardrails**:
  - **No Automatic Silence Cutoffs**: Never add a client-side silence timer (such as a 5-second auto-`passTurn()` timer) in [`web/static/js/audio-call.js`](web/static/js/audio-call.js). A turn may only end when the learner says a trigger phrase (`"Your turn!"`, `"Over to you"`, `"What do you think?"`, `"Go ahead"`) or explicitly taps the handover button.
  - **No Function Tools on Trigger Helper**: In [`internal/ws/hub.go`](internal/ws/hub.go) (`DetectVoiceTriggerLive`), never configure function declarations/tools on the helper session. Always send the last ~4 seconds of speech as `ClientContent` (`MimeType: "audio/wav"` + verbatim transcription instruction) and match the returned transcript via `MatchesVoiceTrigger`.
  - **Cancel on Speech Resume (`triggerCancel` & `stillPaused`)**: In [`internal/ws/session.go`](internal/ws/session.go), immediately cancel any in-flight trigger check (`s.triggerCancel`) the instant `isSpeech` becomes `true`, and verify `stillPaused` (`s.silenceChunksAfterPhrase >= 5` and `!s.turnEnding`) before calling `endUserTurn`.

## 3. Mobile Speakerphone Hardware Teardown (`audio-call.js`)
- **Root Cause**: On mobile browsers (iOS Safari and Android Chrome), keeping the `getUserMedia` microphone stream (`this.micStream`) active while Sarah is speaking forces the mobile OS into **Voice Call / Handset Earpiece Mode** (`play-and-record`), routing audio to the quiet top earpiece instead of the bottom stereo media loudspeaker.
- **Mandatory Guardrails**:
  - In [`web/static/js/audio-call.js`](web/static/js/audio-call.js), whenever on a mobile device in speakerphone mode (`isMobile && this.isSpeakerphone`):
    - Call `this.stopMicrophoneCapture()` whenever `state !== 'listening'` (during Sarah's initial greeting, `'thinking'`, `'speaking'`, and inside `passTurn()`) so the mobile OS releases the voice-call audio lock and plays Sarah's voice through the main stereo loudspeaker.
    - Call `await this.startMicrophoneCapture()` (which attaches `this.setupMicPreAmpChain` with `3.0x` pre-amp + soft limiter compressor) when transitioning back to `'listening'`.
  - Preserve the mobile speakerphone software playback gain (`2.8x` on `this.gainNode`) paired with `this.compressorNode` (`DynamicsCompressorNode`) to ensure loud, distortion-free playback on phones.
