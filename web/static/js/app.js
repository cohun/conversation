// Antigravity English Teacher & Voice Companion - Main Application Logic

document.addEventListener('DOMContentLoaded', () => {
  const App = {
    profile: null,
    personas: [],
    topics: [],
    settings: null,
    currentCall: null,
    activePersona: null,
    micTestStream: null,
    micTestCtx: null,

    init() {
      this.bindEvents();
      this.loadAll();
    },

    async loadAll() {
      await Promise.all([
        this.fetchProfile(),
        this.fetchPersonas(),
        this.fetchTopics(),
        this.fetchSettings(),
      ]);
    },

    bindEvents() {
      // Tab switching
      document.querySelectorAll('.tab-btn').forEach((btn) => {
        btn.addEventListener('click', (e) => {
          const tab = btn.dataset.tab;
          this.switchTab(tab);
        });
      });

      // Header actions & Handshake edit
      document.getElementById('profileBtn').addEventListener('click', () => this.openModal('profileModal'));
      document.getElementById('handshakeEditBtn').addEventListener('click', () => this.openModal('profileModal'));
      document.getElementById('settingsBtn').addEventListener('click', () => this.openModal('settingsModal'));
      document.getElementById('newPersonaBtn').addEventListener('click', () => this.openNewPersonaModal());

      // Modal close buttons
      document.querySelectorAll('[data-close]').forEach((btn) => {
        btn.addEventListener('click', () => {
          const modalId = btn.dataset.close;
          this.closeModal(modalId);
        });
      });

      // Close modal on backdrop click
      document.querySelectorAll('.modal-backdrop').forEach((backdrop) => {
        backdrop.addEventListener('click', (e) => {
          if (e.target === backdrop) {
            this.closeModal(backdrop.id);
          }
        });
      });

      // Profile Save
      document.getElementById('saveProfileBtn').addEventListener('click', () => this.saveProfile());

      // Mic Test for Name Handshake
      document.getElementById('micTestBtn').addEventListener('click', () => this.testMicrophoneHandshake());

      // New Persona Mode change -> dynamic default prompt
      document.getElementById('newPersonaMode').addEventListener('change', (e) => {
        const mode = e.target.value;
        const promptArea = document.getElementById('newPersonaPrompt');
        if (mode === 'teacher') {
          promptArea.value = `You are an encouraging English teacher. When the student speaks, follow this 3-phase cycle:
1. Error Correction & Natural Repetition: Gently correct mistakes and repeat the sentence in natural English.
2. Explanation & Practice Drill: Explain the rule briefly and offer a quick drill.
3. Conversational Response: React to the student's idea and ask an open question.
If the student asks to save the topic, invoke the 'save_conversation' tool.`;
        } else {
          promptArea.value = `You are a friendly and witty conversational buddy on a casual phone call.
Do NOT give lessons or correct grammar. Speak as an equal friend with natural expressions, share interesting thoughts, and ask engaging questions.
If the user asks to save the topic, invoke the 'save_conversation' tool.`;
        }
      });

      // Persona Save
      document.getElementById('savePersonaBtn').addEventListener('click', () => this.savePersona());

      // Settings Save
      document.getElementById('saveSettingsBtn').addEventListener('click', () => this.saveSettings());

      // Call Screen Controls
      document.getElementById('muteBtn').addEventListener('click', () => this.toggleCallMute());
      document.getElementById('passTurnBtn').addEventListener('click', () => this.passCallTurn());
      document.getElementById('pulseCoreOrb').addEventListener('click', () => this.passCallTurn());
      document.getElementById('bookmarkBtn').addEventListener('click', () => this.bookmarkCallTopic());
      document.getElementById('endCallBtn').addEventListener('click', () => this.endActiveCall());
    },

    switchTab(tabName) {
      document.querySelectorAll('.tab-btn').forEach((b) => {
        b.classList.toggle('active', b.dataset.tab === tabName);
      });
      document.getElementById('tabPersonas').classList.toggle('active', tabName === 'personas');
      document.getElementById('tabTopics').classList.toggle('active', tabName === 'topics');
    },

    openModal(id) {
      const modal = document.getElementById(id);
      if (modal) {
        modal.classList.add('active');
      }
    },

    closeModal(id) {
      const modal = document.getElementById(id);
      if (modal) {
        modal.classList.remove('active');
      }
      if (id === 'profileModal' && this.micTestStream) {
        this.stopMicTest();
      }
    },

    // --- Profile & Pronunciation Handshake ---
    async fetchProfile() {
      try {
        const res = await fetch('/api/profile');
        const data = await res.json();
        this.profile = data.profile;
        this.renderProfile();
      } catch (err) {
        console.error('Failed to fetch profile:', err);
      }
    },

    renderProfile() {
      if (!this.profile) return;
      const name = this.profile.display_name || 'Alex';
      const initial = name.trim().charAt(0).toUpperCase();
      const phonetic = this.profile.phonetic_note || `Pronounced: ${name}`;

      document.getElementById('userDisplayName').textContent = `Hello, ${name} 👋`;
      document.getElementById('userAvatarBadge').textContent = initial;
      document.getElementById('userPhoneticHint').textContent = phonetic;

      document.getElementById('profileNameInput').value = name;
      document.getElementById('profilePhoneticInput').value = this.profile.phonetic_note || '';
    },

    async saveProfile() {
      const displayName = document.getElementById('profileNameInput').value.trim();
      const phoneticNote = document.getElementById('profilePhoneticInput').value.trim();
      if (!displayName) {
        alert('Please enter your name.');
        return;
      }

      try {
        const res = await fetch('/api/profile', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            display_name: displayName,
            phonetic_note: phoneticNote,
          }),
        });
        if (!res.ok) throw new Error('Failed to save profile');
        this.profile = await res.json();
        this.renderProfile();
        this.closeModal('profileModal');
      } catch (err) {
        alert('Error saving profile: ' + err.message);
      }
    },

    async testMicrophoneHandshake() {
      const statusText = document.getElementById('micStatusText');
      const micBtn = document.getElementById('micTestBtn');

      if (this.micTestStream) {
        this.stopMicTest();
        return;
      }

      try {
        statusText.textContent = 'Listening... Speak your name clearly now!';
        micBtn.innerHTML = `<span>Stop</span>`;
        micBtn.style.background = 'var(--terracotta-500)';

        this.micTestStream = await navigator.mediaDevices.getUserMedia({ audio: true });
        this.micTestCtx = new (window.AudioContext || window.webkitAudioContext)();
        const source = this.micTestCtx.createMediaStreamSource(this.micTestStream);
        const analyser = this.micTestCtx.createAnalyser();
        analyser.fftSize = 256;
        source.connect(analyser);

        const dataArray = new Uint8Array(analyser.frequencyBinCount);
        const checkLevel = () => {
          if (!this.micTestStream) return;
          analyser.getByteFrequencyData(dataArray);
          let sum = 0;
          for (let i = 0; i < dataArray.length; i++) sum += dataArray[i];
          const avg = sum / dataArray.length;
          if (avg > 15) {
            statusText.textContent = `🎙️ Voice detected! Great clarity (Level: ${Math.round(avg)}).`;
          }
          requestAnimationFrame(checkLevel);
        };
        checkLevel();
      } catch (e) {
        statusText.textContent = 'Microphone permission denied or unavailable.';
        this.stopMicTest();
      }
    },

    stopMicTest() {
      if (this.micTestStream) {
        this.micTestStream.getTracks().forEach((t) => t.stop());
        this.micTestStream = null;
      }
      if (this.micTestCtx) {
        this.micTestCtx.close();
        this.micTestCtx = null;
      }
      const statusText = document.getElementById('micStatusText');
      const micBtn = document.getElementById('micTestBtn');
      if (statusText) statusText.textContent = 'Microphone test stopped. Ready!';
      if (micBtn) {
        micBtn.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z"></path></svg><span>Test Mic</span>`;
        micBtn.style.background = 'var(--sage-500)';
      }
    },

    // --- Personas ---
    async fetchPersonas() {
      try {
        const res = await fetch('/api/personas');
        this.personas = await res.json();
        this.renderPersonas();
      } catch (err) {
        console.error('Failed to fetch personas:', err);
      }
    },

    renderPersonas() {
      const container = document.getElementById('personasList');
      document.getElementById('personasCountBadge').textContent = this.personas.length;

      if (!this.personas || this.personas.length === 0) {
        container.innerHTML = `<div class="empty-state"><div class="empty-icon">👩‍🏫</div><p>No companions found.</p></div>`;
        return;
      }

      container.innerHTML = this.personas
        .map((p) => {
          const avatarEmoji = this.getAvatarEmoji(p);
          const modeLabel = p.mode === 'teacher' ? '3-Phase Teacher' : 'Free Chat';
          const modeClass = p.mode === 'teacher' ? 'teacher' : 'free_chat';
          const isDefaultClass = p.is_default ? 'is-default' : '';
          const deleteBtn = !p.is_default
            ? `<button class="delete-persona-btn" onclick="App.deletePersona('${p.id}')" title="Delete Companion">
                 <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
               </button>`
            : '';

          return `
            <div class="persona-card ${isDefaultClass}" data-persona-id="${p.id}">
              <div class="persona-header">
                <div class="persona-avatar-wrap">${avatarEmoji}</div>
                <div class="persona-details">
                  <div class="persona-title-row">
                    <span class="persona-name">${this.escapeHtml(p.name)}</span>
                    <span class="badge-mode ${modeClass}">${modeLabel}</span>
                    <span class="badge-voice">🎙️ ${this.escapeHtml(p.voice_name)}</span>
                  </div>
                  <p class="persona-desc">${this.escapeHtml(p.short_desc)}</p>
                </div>
              </div>
              <div class="persona-footer">
                <div>${deleteBtn}</div>
                <button class="call-btn" onclick="App.startCall('${p.id}')">
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M22 16.92v3a2 2 0 0 1-2.18 2 19.79 19.79 0 0 1-8.63-3.07 19.5 19.5 0 0 1-6-6 19.79 19.79 0 0 1-3.07-8.67A2 2 0 0 1 4.11 2h3a2 2 0 0 1 2 1.72 12.84 12.84 0 0 0 .7 2.81 2 2 0 0 1-.45 2.11L8.09 9.91a16 16 0 0 0 6 6l1.27-1.27a2 2 0 0 1 2.11-.45 12.84 12.84 0 0 0 2.81.7A2 2 0 0 1 22 16.92z"></path>
                  </svg>
                  <span>Call ${this.escapeHtml(p.name)}</span>
                </button>
              </div>
            </div>
          `;
        })
        .join('');
    },

    getAvatarEmoji(p) {
      if (p.avatar_icon === 'sarah' || p.name === 'Sarah') return '👩‍🏫';
      if (p.avatar_icon === 'leo' || p.name === 'Leo') return '🧔‍♂️';
      if (p.avatar_icon === 'maya' || p.name === 'Maya') return '👩‍💼';
      return p.mode === 'teacher' ? '🧑‍🏫' : '🎙️';
    },

    openNewPersonaModal() {
      document.getElementById('newPersonaName').value = '';
      document.getElementById('newPersonaDesc').value = '';
      document.getElementById('newPersonaMode').value = 'teacher';
      document.getElementById('newPersonaVoice').value = 'Aoede';
      document.getElementById('newPersonaPrompt').value = `You are an encouraging English teacher. When the student speaks, follow this 3-phase cycle:
1. Error Correction & Natural Repetition: Gently correct mistakes and repeat the sentence in natural English.
2. Explanation & Practice Drill: Explain the rule briefly and offer a quick drill.
3. Conversational Response: React to the student's idea and ask an open question.
If the student asks to save the topic, invoke the 'save_conversation' tool.`;
      this.openModal('personaModal');
    },

    async savePersona() {
      const name = document.getElementById('newPersonaName').value.trim();
      const shortDesc = document.getElementById('newPersonaDesc').value.trim();
      const mode = document.getElementById('newPersonaMode').value;
      const voiceName = document.getElementById('newPersonaVoice').value;
      const systemPrompt = document.getElementById('newPersonaPrompt').value.trim();

      if (!name || !systemPrompt) {
        alert('Please provide at least a name and system instructions.');
        return;
      }

      try {
        const res = await fetch('/api/personas', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            name,
            short_desc: shortDesc || 'Conversational companion',
            mode,
            voice_name: voiceName,
            system_prompt: systemPrompt,
          }),
        });
        if (!res.ok) throw new Error('Failed to create persona');
        await this.fetchPersonas();
        this.closeModal('personaModal');
      } catch (err) {
        alert('Error creating persona: ' + err.message);
      }
    },

    async deletePersona(id) {
      if (!confirm('Are you sure you want to delete this companion?')) return;
      try {
        const res = await fetch(`/api/personas/${id}`, { method: 'DELETE' });
        if (!res.ok) throw new Error('Failed to delete persona');
        await this.fetchPersonas();
      } catch (err) {
        alert('Error deleting persona: ' + err.message);
      }
    },

    // --- Saved Topics ---
    async fetchTopics() {
      try {
        const res = await fetch('/api/conversations');
        const data = await res.json();
        this.topics = Array.isArray(data) ? data : [];
        this.renderTopics();
      } catch (err) {
        console.error('Failed to fetch topics:', err);
      }
    },

    renderTopics() {
      const container = document.getElementById('topicsList');
      document.getElementById('topicsCountBadge').textContent = this.topics.length;

      if (!this.topics || this.topics.length === 0) {
        container.innerHTML = `
          <div class="empty-state">
            <div class="empty-icon">📚</div>
            <p>No saved topics yet.</p>
            <p style="font-size:0.8rem; margin-top:6px;">During a call, tap "Bookmark" or say <em>"Let's save this topic for later"</em> to resume anytime!</p>
          </div>
        `;
        return;
      }

      container.innerHTML = this.topics
        .map((t) => {
          const dateStr = t.last_resumed_at ? `Resumed: ${t.last_resumed_at}` : `Saved: ${t.created_at}`;
          const keyPointsHtml = t.key_points
            ? `<div class="topic-pills">${t.key_points
                .split(',')
                .map((k) => `<span class="topic-pill">${this.escapeHtml(k.trim())}</span>`)
                .join('')}</div>`
            : '';
          const openQHtml = t.open_question
            ? `<div class="topic-open-q">" ${this.escapeHtml(t.open_question)} "</div>`
            : '';

          return `
            <div class="topic-card">
              <div class="topic-header">
                <div>
                  <h3 class="topic-title">${this.escapeHtml(t.topic_title)}</h3>
                  <div class="topic-date">With ${this.escapeHtml(t.persona_name || 'Companion')} • ${dateStr}</div>
                </div>
                <button class="delete-topic-btn" onclick="App.deleteTopic('${t.id}')" title="Delete Saved Topic">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
                </button>
              </div>
              <p class="topic-summary">${this.escapeHtml(t.summary)}</p>
              ${keyPointsHtml}
              ${openQHtml}
              <div class="topic-actions">
                <span></span>
                <button class="resume-btn" onclick="App.startCall('${t.persona_id}', '${t.id}')">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M22 16.92v3a2 2 0 0 1-2.18 2 19.79 19.79 0 0 1-8.63-3.07 19.5 19.5 0 0 1-6-6 19.79 19.79 0 0 1-3.07-8.67A2 2 0 0 1 4.11 2h3a2 2 0 0 1 2 1.72 12.84 12.84 0 0 0 .7 2.81 2 2 0 0 1-.45 2.11L8.09 9.91a16 16 0 0 0 6 6l1.27-1.27a2 2 0 0 1 2.11-.45 12.84 12.84 0 0 0 2.81.7A2 2 0 0 1 22 16.92z"></path></svg>
                  <span>Resume Call</span>
                </button>
              </div>
            </div>
          `;
        })
        .join('');
    },

    async deleteTopic(id) {
      if (!confirm('Delete this saved conversation topic?')) return;
      try {
        const res = await fetch(`/api/conversations/${id}`, { method: 'DELETE' });
        if (!res.ok) throw new Error('Failed to delete topic');
        await this.fetchTopics();
      } catch (err) {
        alert('Error deleting topic: ' + err.message);
      }
    },

    // --- Settings & Model Engine ---
    async fetchSettings() {
      try {
        const res = await fetch('/api/settings');
        this.settings = await res.json();
        this.renderSettings();
      } catch (err) {
        console.error('Failed to fetch settings:', err);
      }
    },

    renderSettings() {
      if (!this.settings) return;
      const keyHint = document.getElementById('apiKeyStatusHint');
      if (this.settings.has_api_key) {
        keyHint.textContent = `Active API Key configured (${this.settings.masked_api_key}).`;
        keyHint.style.color = 'var(--sage-600)';
      } else {
        keyHint.textContent = '⚠️ API Key is not set. Please enter your Gemini API Key below.';
        keyHint.style.color = 'var(--danger-500)';
      }

      document.getElementById('settingsModelSelect').value = this.settings.default_live_model || 'auto';
      document.getElementById('activeModelBadge').textContent = this.settings.active_model || 'models/gemini-2.0-flash-exp';
    },

    async saveSettings() {
      const apiKey = document.getElementById('settingsApiKey').value.trim();
      const model = document.getElementById('settingsModelSelect').value;

      const payload = {
        default_live_model: model,
      };
      if (apiKey) {
        payload.gemini_api_key = apiKey;
      }

      try {
        const res = await fetch('/api/settings', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        if (!res.ok) throw new Error('Failed to save settings');
        await this.fetchSettings();
        this.closeModal('settingsModal');
        alert('Settings saved successfully.');
      } catch (err) {
        alert('Error saving settings: ' + err.message);
      }
    },

    // ==========================================
    // Active Phone Call Logic
    // ==========================================
    async startCall(personaId, resumeId = '') {
      // Check if API key is present
      if (this.settings && !this.settings.has_api_key) {
        const entered = prompt('Please enter your Gemini API Key to start voice calls:');
        if (!entered) return;
        await fetch('/api/settings', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ gemini_api_key: entered.trim() }),
        });
        await this.fetchSettings();
      }

      const persona = this.personas.find((p) => p.id === personaId) || {
        id: 'teacher-sarah',
        name: 'Sarah',
        voice_name: 'Aoede',
        mode: 'teacher',
      };
      this.activePersona = persona;

      // Update Call Overlay Header
      document.getElementById('callerAvatar').textContent = this.getAvatarEmoji(persona);
      document.getElementById('callerName').textContent = persona.name;
      document.getElementById('callerBadge').textContent = persona.mode === 'teacher' ? '3-Phase Teacher Mode' : 'Free Chat Mode';
      document.getElementById('callStatusText').textContent = 'Connecting...';
      document.getElementById('callTimer').style.display = 'none';

      // Show Fullscreen Overlay
      const overlay = document.getElementById('callOverlay');
      overlay.classList.add('active');
      this.updateCallVisualState('connecting');

      // Reset mute button
      const muteBtn = document.getElementById('muteBtn');
      muteBtn.classList.remove('active');
      document.getElementById('muteLabel').textContent = 'Mute';

      // Start Audio Call Manager
      this.currentCall = new AudioCallManager({
        personaId: persona.id,
        resumeId: resumeId,
        onStateChange: (state) => this.updateCallVisualState(state),
        onTimerUpdate: (formatted) => {
          const timerEl = document.getElementById('callTimer');
          timerEl.textContent = `• ${formatted}`;
          timerEl.style.display = 'inline';
        },
        onTopicSaved: (topic) => {
          this.showCallToast(`Saved: "${topic.topic_title}" 🔖`);
          this.fetchTopics();
        },
        onError: (errMsg) => {
          alert('Call error: ' + errMsg);
        },
        onCallEnded: () => {
          overlay.classList.remove('active');
          this.currentCall = null;
        },
      });

      await this.currentCall.start();
    },

    updateCallVisualState(state) {
      const overlay = document.getElementById('callOverlay');
      overlay.dataset.callState = state;

      const statusText = document.getElementById('callStatusText');
      const stateLabel = document.getElementById('callStateLabel');
      const bargeInTip = document.getElementById('callBargeInTip');

      switch (state) {
        case 'connecting':
          statusText.textContent = 'Connecting...';
          stateLabel.textContent = 'Establishing voice connection...';
          bargeInTip.textContent = 'Connecting to voice session...';
          this.setPassTurnButtonMode('pass');
          break;

        case 'listening':
          statusText.textContent = 'Connected';
          stateLabel.textContent = 'Listening to you...';
          bargeInTip.textContent = 'Speak anytime, then tap "Átadom a szót" for the response!';
          this.setPassTurnButtonMode('pass');
          break;

        case 'speaking':
          statusText.textContent = 'Connected';
          stateLabel.textContent = `${this.activePersona ? this.activePersona.name : 'Partner'} is speaking...`;
          bargeInTip.textContent = 'Tap "Félbeszakítás" (or the orb) to interrupt!';
          this.setPassTurnButtonMode('interrupt');
          break;

        case 'thinking':
          statusText.textContent = 'Connected';
          stateLabel.textContent = `${this.activePersona ? this.activePersona.name : 'Partner'} is thinking...`;
          bargeInTip.textContent = 'Preparing response...';
          this.setPassTurnButtonMode('pass');
          break;

        case 'ended':
          statusText.textContent = 'Call ended';
          stateLabel.textContent = 'Call completed';
          this.setPassTurnButtonMode('pass');
          break;
      }
    },

    setPassTurnButtonMode(mode) {
      const btn = document.getElementById('passTurnBtn');
      const label = document.getElementById('passTurnLabel');
      const icon = document.getElementById('passTurnIcon');
      if (!btn) return;

      if (mode === 'interrupt') {
        btn.classList.remove('primary-pass');
        btn.classList.add('interrupt-mode');
        btn.title = 'Félbeszakítás / Interrupt';
        if (label) label.textContent = 'Félbeszakítás';
        if (icon) {
          icon.innerHTML = `<rect x="6" y="6" width="12" height="12" rx="2" fill="currentColor"></rect>`;
        }
      } else {
        btn.classList.remove('interrupt-mode');
        btn.classList.add('primary-pass');
        btn.title = 'Átadom a szót / Your Turn';
        if (label) label.textContent = 'Átadom a szót';
        if (icon) {
          icon.innerHTML = `<polyline points="9 18 15 12 9 6"></polyline>`;
        }
      }
    },

    showCallToast(text) {
      const toast = document.getElementById('callToast');
      toast.textContent = text;
      toast.style.display = 'block';
      setTimeout(() => {
        toast.style.display = 'none';
      }, 4000);
    },

    toggleCallMute() {
      if (!this.currentCall) return;
      const isMuted = this.currentCall.toggleMute();
      const muteBtn = document.getElementById('muteBtn');
      const muteLabel = document.getElementById('muteLabel');
      muteBtn.classList.toggle('active', isMuted);
      muteLabel.textContent = isMuted ? 'Unmute' : 'Mute';
    },

    passCallTurn() {
      if (!this.currentCall) return;
      this.currentCall.passTurn();
    },

    bookmarkCallTopic() {
      if (!this.currentCall) return;
      this.currentCall.bookmarkTopic();
    },

    endActiveCall() {
      if (this.currentCall) {
        this.currentCall.endCall();
      }
    },

    escapeHtml(str) {
      if (!str) return '';
      return String(str)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
    },
  };

  window.App = App;
  App.init();
});
