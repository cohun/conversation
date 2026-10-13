// Antigravity English Teacher & Voice Companion - Main Application Logic

document.addEventListener('DOMContentLoaded', () => {
  const App = {
    profile: null,
    currentUser: null,
    authMode: 'login', // 'login' or 'register'
    adminUsers: [],
    editingPersonaId: null,
    personas: [],
    topics: [],
    settings: null,
    currentCall: null,
    activePersona: null,
    micTestStream: null,
    micTestCtx: null,

    async init() {
      this.bindEvents();
      this.checkURLParams();
      await this.loadAll();
    },

    async loadAll() {
      await this.fetchMe();
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
      const profileBtn = document.getElementById('profileBtn');
      if (profileBtn) {
        profileBtn.addEventListener('click', () => this.openModal('profileModal'));
      }

      const guideBtn = document.getElementById('guideBtn');
      if (guideBtn) {
        guideBtn.addEventListener('click', () => this.openModal('guideModal'));
      }

      document.getElementById('handshakeEditBtn').addEventListener('click', () => {
        if (!this.currentUser) {
          this.openAuthModal('login');
        } else {
          this.openModal('profileModal');
        }
      });

      const loginTriggerBtn = document.getElementById('loginTriggerBtn');
      if (loginTriggerBtn) {
        loginTriggerBtn.addEventListener('click', () => this.openAuthModal('login'));
      }

      const adminBtn = document.getElementById('adminBtn');
      if (adminBtn) {
        adminBtn.addEventListener('click', () => this.openAdminDashboard());
      }

      const settingsBtn = document.getElementById('settingsBtn');
      if (settingsBtn) {
        settingsBtn.addEventListener('click', () => this.openModal('settingsModal'));
      }
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

      // Auth Modal events
      const authTabLogin = document.getElementById('authTabLogin');
      const authTabRegister = document.getElementById('authTabRegister');
      if (authTabLogin) authTabLogin.addEventListener('click', () => this.setAuthMode('login'));
      if (authTabRegister) authTabRegister.addEventListener('click', () => this.setAuthMode('register'));

      const authSubmitBtn = document.getElementById('authSubmitBtn');
      if (authSubmitBtn) authSubmitBtn.addEventListener('click', () => this.submitAuthForm());

      const authForm = document.getElementById('authForm');
      if (authForm) {
        authForm.addEventListener('keydown', (e) => {
          if (e.key === 'Enter') {
            e.preventDefault();
            this.submitAuthForm();
          }
        });
      }

      // Logout event
      const logoutBtn = document.getElementById('logoutBtn');
      if (logoutBtn) logoutBtn.addEventListener('click', () => this.logout());

      // BYOK Custom Key events
      const saveCustomKeyBtn = document.getElementById('saveCustomKeyBtn');
      if (saveCustomKeyBtn) saveCustomKeyBtn.addEventListener('click', () => this.saveCustomKey());

      const removeCustomKeyBtn = document.getElementById('removeCustomKeyBtn');
      if (removeCustomKeyBtn) removeCustomKeyBtn.addEventListener('click', () => this.removeCustomKey());

      // Admin Dashboard events
      const adminRefreshBtn = document.getElementById('adminRefreshBtn');
      if (adminRefreshBtn) adminRefreshBtn.addEventListener('click', () => this.fetchAdminUsers());

      // Pending & Blocked Screen events
      const pendingRefreshBtn = document.getElementById('pendingRefreshBtn');
      if (pendingRefreshBtn) {
        pendingRefreshBtn.addEventListener('click', async () => {
          pendingRefreshBtn.disabled = true;
          const origText = pendingRefreshBtn.innerHTML;
          pendingRefreshBtn.innerHTML = `<span>Ellenőrzés...</span>`;
          await this.fetchMe();
          pendingRefreshBtn.disabled = false;
          pendingRefreshBtn.innerHTML = origText;
        });
      }

      const pendingLogoutBtn = document.getElementById('pendingLogoutBtn');
      if (pendingLogoutBtn) pendingLogoutBtn.addEventListener('click', () => this.logout());

      const blockedLogoutBtn = document.getElementById('blockedLogoutBtn');
      if (blockedLogoutBtn) blockedLogoutBtn.addEventListener('click', () => this.logout());

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

      // Persona Modal Reset to Default
      const resetModalBtn = document.getElementById('resetPersonaModalBtn');
      if (resetModalBtn) {
        resetModalBtn.addEventListener('click', () => {
          if (this.editingPersonaId) {
            const p = this.personas.find((item) => item.id === this.editingPersonaId);
            const name = p ? p.name : 'Companion';
            this.resetPersona(this.editingPersonaId, name);
          }
        });
      }

      // Settings Save
      document.getElementById('saveSettingsBtn').addEventListener('click', () => this.saveSettings());

      // Call Screen Controls
      document.getElementById('muteBtn').addEventListener('click', () => this.toggleCallMute());
      const speakerBtn = document.getElementById('speakerBtn');
      if (speakerBtn) {
        speakerBtn.addEventListener('click', () => this.toggleCallSpeaker());
      }
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

    checkURLParams() {
      const params = new URLSearchParams(window.location.search);
      if (params.get('auth_error')) {
        const err = params.get('auth_error');
        if (err === 'google_oauth_not_configured') {
          this.openAuthModal('login');
          const errorBox = document.getElementById('authErrorMsg');
          if (errorBox) {
            errorBox.innerHTML = `<strong>A Google bejelentkezés még nincs beállítva a szerveren.</strong><br>Kérlek regisztrálj vagy jelentkezz be alább az email címeddel!<br><small>Az <code>attila.hitb@gmail.com</code> és <code>attila.private@gmail.com</code> címek azonnal Rendszergazda jogot kapnak.</small>`;
            errorBox.style.display = 'block';
          }
        } else {
          alert('Bejelentkezési hiba: ' + err);
        }
        window.history.replaceState({}, document.title, window.location.pathname);
      } else if (params.get('auth_success')) {
        window.history.replaceState({}, document.title, window.location.pathname);
      }
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

    // --- Authentication (Login & Register) ---
    openAuthModal(mode = 'login') {
      this.setAuthMode(mode);
      this.openModal('authModal');
    },

    setAuthMode(mode) {
      this.authMode = mode;
      const tabLogin = document.getElementById('authTabLogin');
      const tabRegister = document.getElementById('authTabRegister');
      const nameGroup = document.getElementById('authNameGroup');
      const title = document.getElementById('authModalTitle');
      const submitBtn = document.getElementById('authSubmitBtn');
      const errorBox = document.getElementById('authErrorMsg');

      if (errorBox) {
        errorBox.style.display = 'none';
        errorBox.textContent = '';
      }

      if (mode === 'register') {
        if (tabLogin) tabLogin.classList.remove('active');
        if (tabRegister) tabRegister.classList.add('active');
        if (nameGroup) nameGroup.style.display = 'flex';
        if (title) title.textContent = 'Új Regisztráció';
        if (submitBtn) submitBtn.textContent = 'Fiók létrehozása';
      } else {
        if (tabLogin) tabLogin.classList.add('active');
        if (tabRegister) tabRegister.classList.remove('active');
        if (nameGroup) nameGroup.style.display = 'none';
        if (title) title.textContent = 'Bejelentkezés';
        if (submitBtn) submitBtn.textContent = 'Bejelentkezés';
      }
    },

    async submitAuthForm() {
      const emailInput = document.getElementById('authEmailInput');
      const passInput = document.getElementById('authPasswordInput');
      const nameInput = document.getElementById('authNameInput');
      const errorBox = document.getElementById('authErrorMsg');

      const email = emailInput ? emailInput.value.trim() : '';
      const password = passInput ? passInput.value : '';
      const name = nameInput ? nameInput.value.trim() : '';

      if (!email || !password) {
        if (errorBox) {
          errorBox.textContent = 'Kérlek add meg az email címedet és a jelszavadat.';
          errorBox.style.display = 'block';
        }
        return;
      }

      if (this.authMode === 'register' && password.length < 6) {
        if (errorBox) {
          errorBox.textContent = 'A jelszónak legalább 6 karakter hosszúnak kell lennie.';
          errorBox.style.display = 'block';
        }
        return;
      }

      const submitBtn = document.getElementById('authSubmitBtn');
      const originalText = submitBtn ? submitBtn.textContent : '';
      if (submitBtn) {
        submitBtn.disabled = true;
        submitBtn.textContent = 'Kapcsolódás...';
      }

      try {
        const endpoint = this.authMode === 'register' ? '/api/auth/register' : '/api/auth/login';
        const payload = { email, password };
        if (this.authMode === 'register') {
          payload.display_name = name;
          payload.name = name;
        }

        const res = await fetch(endpoint, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });

        const data = await res.json();
        if (!res.ok) {
          throw new Error(data.error || 'Azonosítási hiba történt');
        }

        this.closeModal('authModal');
        await this.loadAll();
      } catch (err) {
        if (errorBox) {
          errorBox.textContent = err.message;
          errorBox.style.display = 'block';
        }
      } finally {
        if (submitBtn) {
          submitBtn.disabled = false;
          submitBtn.textContent = originalText;
        }
      }
    },

    async logout() {
      try {
        await fetch('/api/auth/logout', { method: 'POST' });
        this.currentUser = null;
        this.closeModal('profileModal');
        this.closeModal('adminModal');
        await this.loadAll();
      } catch (err) {
        console.error('Logout error:', err);
      }
    },

    // --- User State & Quota UI ---
    async fetchMe() {
      try {
        const res = await fetch('/api/auth/me');
        const data = await res.json();
        if (data.authenticated && data.user) {
          this.currentUser = data.user;
        } else {
          this.currentUser = null;
        }
        this.renderUserUI();
      } catch (err) {
        console.error('Failed to fetch user state:', err);
        this.currentUser = null;
        this.renderUserUI();
      }
    },

    renderUserUI() {
      const loginTriggerBtn = document.getElementById('loginTriggerBtn');
      const profileBtn = document.getElementById('profileBtn');
      const adminBtn = document.getElementById('adminBtn');
      const statusAlert = document.getElementById('accountStatusAlert');
      const userQuotaPill = document.getElementById('userQuotaPill');
      const userStatusBar = document.getElementById('userStatusBar');
      const activeDashboard = document.getElementById('activeDashboard');
      const pendingScreen = document.getElementById('pendingApprovalScreen');
      const blockedScreen = document.getElementById('blockedScreen');
      const pendingWelcomeTitle = document.getElementById('pendingWelcomeTitle');

      if (this.currentUser) {
        if (loginTriggerBtn) loginTriggerBtn.style.display = 'none';
        if (profileBtn) profileBtn.style.display = 'inline-flex';

        // Admin badge button in header
        if (adminBtn) {
          adminBtn.style.display = this.currentUser.role === 'admin' ? 'inline-flex' : 'none';
        }

        // Display name & avatar initial
        const rawName = this.currentUser.display_name || this.currentUser.name || this.currentUser.email.split('@')[0];
        const name = rawName.replace(/[._-]/g, ' ').replace(/\s+/g, ' ').trim();
        const initial = name.charAt(0).toUpperCase();

        // Dynamic Brand Title: [User Name]'s Live English Companion
        const brandTitle = document.getElementById('brandTitle');
        if (brandTitle) {
          brandTitle.textContent = `${name}'s Live English Companion`;
        }
        document.title = `${name}'s Live English Companion`;

        // Screen routing based on account status
        if (this.currentUser.status === 'pending') {
          // Welcome / Waiting screen: hide active learning dashboard and status bar
          if (userStatusBar) userStatusBar.style.display = 'none';
          if (activeDashboard) activeDashboard.style.display = 'none';
          if (statusAlert) statusAlert.style.display = 'none';
          if (blockedScreen) blockedScreen.style.display = 'none';
          if (pendingScreen) pendingScreen.style.display = 'block';
          if (pendingWelcomeTitle) pendingWelcomeTitle.textContent = `Üdvözlünk, ${this.escapeHtml(name)}! 👋`;
        } else if (this.currentUser.status === 'blocked') {
          // Blocked screen only
          if (userStatusBar) userStatusBar.style.display = 'none';
          if (activeDashboard) activeDashboard.style.display = 'none';
          if (statusAlert) statusAlert.style.display = 'none';
          if (pendingScreen) pendingScreen.style.display = 'none';
          if (blockedScreen) blockedScreen.style.display = 'block';
        } else {
          // Active user: full access to learning dashboard
          if (userStatusBar) userStatusBar.style.display = 'flex';
          if (activeDashboard) activeDashboard.style.display = 'block';
          if (pendingScreen) pendingScreen.style.display = 'none';
          if (blockedScreen) blockedScreen.style.display = 'none';
          if (statusAlert) statusAlert.style.display = 'none';
        }

        document.getElementById('userDisplayName').textContent = `Hello, ${this.escapeHtml(name)} 👋`;
        document.getElementById('userAvatarBadge').textContent = initial;

        const phonetic = this.currentUser.phonetic_note || `Pronounced: ${name}`;
        document.getElementById('userPhoneticHint').textContent = phonetic;

        // Quota pill in banner
        if (userQuotaPill) {
          if (this.currentUser.role === 'admin') {
            userQuotaPill.textContent = '👑 Korlátlan (Admin)';
            userQuotaPill.className = 'user-quota-pill unlimited';
          } else if (this.currentUser.has_custom_key) {
            userQuotaPill.textContent = '🔑 Korlátlan (BYOK)';
            userQuotaPill.className = 'user-quota-pill unlimited';
          } else {
            const used = Math.round(this.currentUser.today_used_minutes || 0);
            const limit = this.currentUser.today_limit_minutes || 45;
            userQuotaPill.textContent = `⏱️ ${used} / ${limit} perc`;
            userQuotaPill.className = used >= limit ? 'user-quota-pill exceeded' : 'user-quota-pill';
          }
        }

        // Populate Profile Modal details
        const emailEl = document.getElementById('profileEmailDisplay');
        if (emailEl) emailEl.textContent = this.currentUser.email;

        const roleBadge = document.getElementById('profileRoleBadge');
        if (roleBadge) {
          roleBadge.textContent = this.currentUser.role === 'admin' ? '👑 Rendszergazda' : 'Tanuló';
          roleBadge.className = this.currentUser.role === 'admin' ? 'profile-role-badge admin' : 'profile-role-badge';
        }

        const statusBadge = document.getElementById('profileStatusBadge');
        if (statusBadge) {
          const statusMap = { active: 'Aktív', pending: 'Jóváhagyásra vár', blocked: 'Felfüggesztve' };
          statusBadge.textContent = statusMap[this.currentUser.status] || this.currentUser.status;
          statusBadge.className = `profile-status-badge ${this.currentUser.status}`;
        }

        const quotaText = document.getElementById('profileQuotaText');
        const quotaBar = document.getElementById('profileQuotaBar');
        const quotaHint = document.getElementById('profileQuotaHint');

        if (this.currentUser.role === 'admin') {
          if (quotaText) quotaText.textContent = 'Adminisztrátor (Korlátlan)';
          if (quotaBar) quotaBar.style.width = '100%';
          if (quotaHint) quotaHint.textContent = 'Rendszergazdaként korlátlan beszédidővel rendelkezel.';
        } else if (this.currentUser.has_custom_key) {
          if (quotaText) quotaText.textContent = 'Saját kulcs aktív (Korlátlan)';
          if (quotaBar) quotaBar.style.width = '100%';
          if (quotaHint) quotaHint.textContent = 'A saját Google AI Studio kulcsodat használod, a hívásaid korlátlanok.';
        } else {
          const used = Math.round(this.currentUser.today_used_minutes || 0);
          const limit = this.currentUser.today_limit_minutes || 45;
          const pct = Math.min(100, Math.round((used / limit) * 100));
          if (quotaText) quotaText.textContent = `${used} / ${limit} perc (${pct}%)`;
          if (quotaBar) quotaBar.style.width = `${pct}%`;
          if (quotaHint) {
            quotaHint.textContent = used >= limit
              ? '⚠️ A mai központi kereted elfogyott! Állíts be saját ingyenes Google kulcsot alább a folytatáshoz.'
              : 'A központi keret minden éjfélkor automatikusan újraindul.';
          }
        }

        // BYOK custom key status
        const customBadge = document.getElementById('customKeyBadge');
        const removeKeyBtn = document.getElementById('removeCustomKeyBtn');
        const keyInput = document.getElementById('customKeyInput');
        if (this.currentUser.has_custom_key) {
          if (customBadge) {
            customBadge.textContent = `✅ Aktív (${this.currentUser.masked_custom_key})`;
            customBadge.className = 'byok-status-pill active';
          }
          if (removeKeyBtn) removeKeyBtn.style.display = 'inline-flex';
          if (keyInput) keyInput.placeholder = 'Új kulcs megadása a cseréhez...';
        } else {
          if (customBadge) {
            customBadge.textContent = 'Nincs egyéni kulcs';
            customBadge.className = 'byok-status-pill none';
          }
          if (removeKeyBtn) removeKeyBtn.style.display = 'none';
          if (keyInput) keyInput.placeholder = 'AIzaSy...';
        }

      } else {
        // Unauthenticated / Guest
        if (loginTriggerBtn) loginTriggerBtn.style.display = 'inline-flex';
        if (profileBtn) profileBtn.style.display = 'none';
        if (adminBtn) adminBtn.style.display = 'none';
        if (statusAlert) statusAlert.style.display = 'none';

        if (userStatusBar) userStatusBar.style.display = 'flex';
        if (activeDashboard) activeDashboard.style.display = 'block';
        if (pendingScreen) pendingScreen.style.display = 'none';
        if (blockedScreen) blockedScreen.style.display = 'none';

        // Reset Brand Title for guest
        const brandTitle = document.getElementById('brandTitle');
        if (brandTitle) {
          brandTitle.textContent = 'Live English Companion';
        }
        document.title = 'Live English Companion';

        document.getElementById('userDisplayName').textContent = 'Vendég 👋';
        document.getElementById('userAvatarBadge').textContent = 'V';
        document.getElementById('userPhoneticHint').textContent = 'Kattints a Belépés gombra a beszélgetéshez';
        if (userQuotaPill) {
          userQuotaPill.textContent = 'Belépés szükséges';
          userQuotaPill.className = 'user-quota-pill';
        }
      }
    },

    // --- BYOK (Bring Your Own Key) ---
    async saveCustomKey() {
      const keyInput = document.getElementById('customKeyInput');
      const key = keyInput ? keyInput.value.trim() : '';
      const errorHint = document.getElementById('customKeyError');
      const saveBtn = document.getElementById('saveCustomKeyBtn');

      if (!key) {
        alert('Kérlek illeszd be a Google Gemini API kulcsodat!');
        return;
      }

      if (errorHint) errorHint.style.display = 'none';
      const origText = saveBtn ? saveBtn.textContent : '';
      if (saveBtn) {
        saveBtn.disabled = true;
        saveBtn.textContent = 'Ellenőrzés Google AI Studio-val...';
      }

      try {
        const res = await fetch('/api/user/custom-key', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ api_key: key }),
        });
        const data = await res.json();
        if (!res.ok) {
          throw new Error(data.error || 'A Google API elutasította a megadott kulcsot.');
        }

        alert('✅ Saját Google API kulcs sikeresen ellenőrizve és aktiválva!\n\nMostantól korlátlanul beszélgethetsz a saját kulcsoddal.');
        if (keyInput) keyInput.value = '';
        await this.fetchMe();
      } catch (err) {
        if (errorHint) {
          errorHint.textContent = '❌ ' + err.message;
          errorHint.style.display = 'block';
        } else {
          alert('Hiba: ' + err.message);
        }
      } finally {
        if (saveBtn) {
          saveBtn.disabled = false;
          saveBtn.textContent = origText;
        }
      }
    },

    async removeCustomKey() {
      if (!confirm('Biztosan törlöd a saját API kulcsodat?\n\nEzután újra a központi napi kvóta (45 perc) lesz érvényes a fiókodra.')) {
        return;
      }
      try {
        const res = await fetch('/api/user/custom-key', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ api_key: '' }),
        });
        if (!res.ok) throw new Error('Nem sikerült törölni a kulcsot');
        await this.fetchMe();
      } catch (err) {
        alert('Hiba a kulcs törlése során: ' + err.message);
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
      const phonetic = this.profile.phonetic_note || `Pronounced: ${name}`;

      if (!this.currentUser) {
        document.getElementById('userDisplayName').textContent = `Hello, ${this.escapeHtml(name)} 👋`;
        document.getElementById('userAvatarBadge').textContent = name.trim().charAt(0).toUpperCase();
        document.getElementById('userPhoneticHint').textContent = phonetic;
      }

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
        await this.fetchMe();
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
          const overrideBadge = (p.is_default && p.has_override)
            ? '<span class="badge-override" title="Egyéni beállítások érvényben">✨ Egyéni</span>'
            : '';
          const resetBtn = (p.is_default && p.has_override)
            ? `<button class="reset-persona-btn" onclick="App.resetPersona('${p.id}', '${this.escapeHtml(p.name)}')" title="Alapbeállítások visszaállítása">
                 <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"></path><path d="M3 3v5h5"></path></svg>
                 <span>Alaphelyzet</span>
               </button>`
            : '';
          const deleteBtn = !p.is_default
            ? `<button class="delete-persona-btn" onclick="App.deletePersona('${p.id}')" title="Companion törlése">
                 <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
               </button>`
            : '';

          return `
            <div class="persona-card ${isDefaultClass}" data-persona-id="${p.id}">
              <div class="persona-header">
                <div class="persona-avatar-wrap">${avatarEmoji}</div>
                <div class="persona-details">
                  <div class="persona-title-row">
                    <span class="persona-name">${this.escapeHtml(p.name)}</span>
                    ${overrideBadge}
                    <span class="badge-mode ${modeClass}">${modeLabel}</span>
                    <span class="badge-voice">🎙️ ${this.escapeHtml(p.voice_name)}</span>
                  </div>
                  <p class="persona-desc">${this.escapeHtml(p.short_desc)}</p>
                </div>
              </div>
              <div class="persona-footer">
                <div class="persona-card-actions">
                  <button class="edit-persona-btn" onclick="App.openEditPersona('${p.id}')" title="Név és beállítások módosítása">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"></path><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"></path></svg>
                    <span>Módosítás</span>
                  </button>
                  ${resetBtn}
                  ${deleteBtn}
                </div>
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
      if (p.avatar_icon === 'fred' || p.name === 'Fred' || p.avatar_icon === 'leo' || p.name === 'Leo') return '🧔‍♂️';
      if (p.avatar_icon === 'maya' || p.name === 'Maya') return '👩‍💼';
      return p.mode === 'teacher' ? '🧑‍🏫' : '🎙️';
    },

    openNewPersonaModal() {
      this.editingPersonaId = null;
      const modal = document.getElementById('personaModal');
      const title = modal ? modal.querySelector('.modal-title') : null;
      if (title) title.textContent = 'Új Companion Létrehozása';
      const saveBtn = document.getElementById('savePersonaBtn');
      if (saveBtn) saveBtn.textContent = 'Létrehozás';

      const notice = document.getElementById('personaOverrideNotice');
      if (notice) notice.style.display = 'none';
      const resetModalBtn = document.getElementById('resetPersonaModalBtn');
      if (resetModalBtn) resetModalBtn.style.display = 'none';

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

    openEditPersona(id) {
      const p = this.personas.find((item) => item.id === id);
      if (!p) return;

      this.editingPersonaId = p.id;
      const modal = document.getElementById('personaModal');
      const title = modal ? modal.querySelector('.modal-title') : null;
      if (title) title.textContent = `Módosítás: ${p.name}`;
      const saveBtn = document.getElementById('savePersonaBtn');
      if (saveBtn) saveBtn.textContent = 'Módosítások mentése';

      const notice = document.getElementById('personaOverrideNotice');
      const resetModalBtn = document.getElementById('resetPersonaModalBtn');

      if (p.is_default) {
        if (notice) notice.style.display = 'flex';
        if (resetModalBtn) resetModalBtn.style.display = p.has_override ? 'inline-flex' : 'none';
      } else {
        if (notice) notice.style.display = 'none';
        if (resetModalBtn) resetModalBtn.style.display = 'none';
      }

      document.getElementById('newPersonaName').value = p.name;
      document.getElementById('newPersonaDesc').value = p.short_desc || '';
      document.getElementById('newPersonaMode').value = p.mode || 'teacher';
      document.getElementById('newPersonaVoice').value = p.voice_name || 'Aoede';
      document.getElementById('newPersonaPrompt').value = p.system_prompt || '';

      this.openModal('personaModal');
    },

    async savePersona() {
      const name = document.getElementById('newPersonaName').value.trim();
      const shortDesc = document.getElementById('newPersonaDesc').value.trim();
      const mode = document.getElementById('newPersonaMode').value;
      const voiceName = document.getElementById('newPersonaVoice').value;
      const systemPrompt = document.getElementById('newPersonaPrompt').value.trim();

      if (!name || !systemPrompt) {
        alert('Kérlek adj meg egy nevet és a companion utasításait.');
        return;
      }

      const payload = {
        name,
        short_desc: shortDesc || 'Conversational companion',
        mode,
        voice_name: voiceName,
        system_prompt: systemPrompt,
      };

      try {
        let endpoint = '/api/personas';
        let method = 'POST';
        if (this.editingPersonaId) {
          endpoint = `/api/personas/${encodeURIComponent(this.editingPersonaId)}`;
          method = 'PUT';
        }

        const res = await fetch(endpoint, {
          method,
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        if (!res.ok) throw new Error('Nem sikerült elmenteni a companion adatait.');
        this.editingPersonaId = null;
        await this.fetchPersonas();
        this.closeModal('personaModal');
      } catch (err) {
        alert('Hiba a mentés során: ' + err.message);
      }
    },

    async resetPersona(id, name) {
      const confirmMsg = name
        ? `Biztosan visszaállítod "${name}" alapértelmezett gyári beállításait?`
        : 'Biztosan visszaállítod az eredeti gyári beállításokat?';
      if (!confirm(confirmMsg)) return;

      try {
        const res = await fetch(`/api/personas/${encodeURIComponent(id)}/reset`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
        });
        if (!res.ok) {
          const errData = await res.json().catch(() => ({}));
          throw new Error(errData.error || 'Hiba a visszaállítás során');
        }
        this.closeModal('personaModal');
        await this.fetchPersonas();
      } catch (err) {
        alert('Hiba a visszaállítás során: ' + err.message);
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
    // Admin Dashboard Handlers
    // ==========================================
    async openAdminDashboard() {
      this.openModal('adminModal');
      await this.fetchAdminUsers();
    },

    async fetchAdminUsers() {
      const refreshBtn = document.getElementById('adminRefreshBtn');
      if (refreshBtn) refreshBtn.style.opacity = '0.6';

      try {
        const res = await fetch('/api/admin/users');
        if (!res.ok) {
          const errData = await res.json().catch(() => ({}));
          throw new Error(errData.error || `HTTP ${res.status}: Jogosulatlan hozzáférés vagy admin hiba`);
        }
        this.adminUsers = await res.json();
        this.renderAdminUsers();
      } catch (err) {
        console.error('Failed to fetch admin users:', err);
        alert('Admin hiba: ' + err.message);
      } finally {
        if (refreshBtn) refreshBtn.style.opacity = '1';
      }
    },

    renderAdminUsers() {
      const users = this.adminUsers || [];
      const totalUsers = users.length;
      let totalTodaySec = 0;
      let totalDurationSec = 0;
      let totalCostUSD = 0.0;

      users.forEach((u) => {
        totalTodaySec += u.today_duration_sec || 0;
        totalDurationSec += u.total_duration_sec || 0;
        totalCostUSD += u.total_cost_usd || 0.0;
      });

      const statTotalUsers = document.getElementById('adminStatTotalUsers');
      const statTodayDuration = document.getElementById('adminStatTodayDuration');
      const statTotalDuration = document.getElementById('adminStatTotalDuration');
      const statTotalCost = document.getElementById('adminStatTotalCost');

      if (statTotalUsers) statTotalUsers.textContent = totalUsers;
      if (statTodayDuration) statTodayDuration.textContent = `${Math.round(totalTodaySec / 60)} perc`;
      const totalMin = Math.round(totalDurationSec / 60);
      const totalHours = (totalMin / 60).toFixed(1);
      if (statTotalDuration) statTotalDuration.textContent = `${totalMin} p (~${totalHours} óra)`;
      if (statTotalCost) statTotalCost.textContent = `$${totalCostUSD.toFixed(2)}`;

      const tbody = document.getElementById('adminUsersTableBody');
      if (!tbody) return;

      if (users.length === 0) {
        tbody.innerHTML = `<tr><td colspan="7" style="text-align: center; color: var(--text-muted); padding: 24px;">Nincsenek még regisztrált felhasználók.</td></tr>`;
        return;
      }

      tbody.innerHTML = users
        .map((u) => {
          const isPrimaryAdmin = u.email === 'attila.hitb@gmail.com' || u.email === 'attila.private@gmail.com';
          const authBadge = u.auth_provider === 'google' ? '🌐 Google' : '🔑 Email';
          const todayMin = Math.round((u.today_duration_sec || 0) / 60);
          const totalUserMin = Math.round((u.total_duration_sec || 0) / 60);
          const costStr = `$${(u.total_cost_usd || 0).toFixed(2)}`;
          const byokBadge = u.custom_api_key
            ? `<span class="badge-byok yes" title="Saját Google AI Studio kulcs, neked $0 költség!">Saját kulcs ($0)</span>`
            : `<span class="badge-byok no">Központi keret</span>`;

          const statusOptions = ['active', 'pending', 'blocked']
            .map(
              (s) =>
                `<option value="${s}" ${u.status === s ? 'selected' : ''}>${s.toUpperCase()}</option>`
            )
            .join('');

          const deleteBtn = !isPrimaryAdmin
            ? `<button class="admin-del-btn" onclick="App.adminDeleteUser('${u.id}', '${this.escapeHtml(u.email)}')" title="Felhasználó törlése">
                 <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
               </button>`
            : '<span style="font-size: 0.72rem; color: var(--text-muted);">Fő Admin</span>';

          return `
            <tr>
              <td>
                <div style="font-weight: 700; color: var(--text-primary);">${this.escapeHtml(u.display_name || u.email)}</div>
                <div class="user-email-sub">${this.escapeHtml(u.email)} • ${authBadge}</div>
              </td>
              <td>
                <select class="admin-select" ${(isPrimaryAdmin && u.status === 'active') ? 'disabled' : ''} onchange="App.adminUpdateStatus('${u.id}', this.value)">
                  ${statusOptions}
                </select>
              </td>
              <td>
                <div style="display: flex; align-items: center; gap: 4px;">
                  <input type="number" min="5" max="1440" step="5" class="admin-limit-input" value="${u.daily_limit_minutes || 45}" onchange="App.adminUpdateLimit('${u.id}', this.value)">
                  <span style="font-size: 0.75rem; color: var(--text-muted);">perc</span>
                </div>
              </td>
              <td>${byokBadge}</td>
              <td><strong>${todayMin}</strong> perc</td>
              <td>
                <div>${totalUserMin} perc</div>
                <div style="font-size: 0.72rem; color: var(--sage-600); font-weight: 600;">${costStr}</div>
              </td>
              <td>${deleteBtn}</td>
            </tr>
          `;
        })
        .join('');
    },

    async adminUpdateStatus(userId, newStatus) {
      try {
        const res = await fetch('/api/admin/users/status', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ user_id: userId, status: newStatus }),
        });
        if (!res.ok) {
          const err = await res.json();
          throw new Error(err.error || 'Sikertelen státuszmódosítás');
        }
        await this.fetchAdminUsers();
      } catch (e) {
        alert('Hiba: ' + e.message);
        await this.fetchAdminUsers();
      }
    },

    async adminUpdateLimit(userId, limitMinutes) {
      const minutes = parseInt(limitMinutes, 10);
      if (isNaN(minutes) || minutes < 5 || minutes > 1440) {
        alert('A napi limitnek 5 és 1440 perc között kell lennie.');
        return;
      }
      try {
        const res = await fetch('/api/admin/users/limit', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ user_id: userId, limit_minutes: minutes }),
        });
        if (!res.ok) {
          const err = await res.json();
          throw new Error(err.error || 'Sikertelen limit módosítás');
        }
      } catch (e) {
        alert('Hiba: ' + e.message);
        await this.fetchAdminUsers();
      }
    },

    async adminDeleteUser(userId, email) {
      if (!confirm(`Biztosan törölni szeretnéd a(z) ${email} felhasználót és összes korábbi hívásnaplóját?`)) {
        return;
      }
      try {
        const res = await fetch(`/api/admin/users?user_id=${encodeURIComponent(userId)}`, {
          method: 'DELETE',
        });
        if (!res.ok) {
          const err = await res.json();
          throw new Error(err.error || 'Sikertelen törlés');
        }
        await this.fetchAdminUsers();
      } catch (e) {
        alert('Hiba a törlés során: ' + e.message);
      }
    },

    // ==========================================
    // Active Phone Call Logic
    // ==========================================
    async startCall(personaId, resumeId = '') {
      // 1. Guard: Check Authentication
      if (!this.currentUser) {
        this.openAuthModal('login');
        const errorBox = document.getElementById('authErrorMsg');
        if (errorBox) {
          errorBox.textContent = 'A beszélgetés indításához kérlek jelentkezz be Google-fiókoddal vagy regisztrálj!';
          errorBox.style.display = 'block';
        }
        return;
      }

      // 2. Guard: Check User Status
      if (this.currentUser.status === 'pending') {
        alert('⏳ A fiókod rendszergazdai jóváhagyásra vár.\n\nAmint az admin jóváhagyja, azonnal beszélgethetsz!');
        return;
      }
      if (this.currentUser.status === 'blocked') {
        alert('🚫 A fiókod hozzáférését az adminisztrátor felfüggesztette.');
        return;
      }

      // 3. Guard: Daily Quota Check (unless Admin or BYOK)
      if (
        this.currentUser.role !== 'admin' &&
        !this.currentUser.has_custom_key &&
        this.currentUser.today_used_minutes >= this.currentUser.today_limit_minutes
      ) {
        alert(
          '⏱️ A mai ingyenes beszédkereted (' + this.currentUser.today_limit_minutes + ' perc) elfogyott!\n\n' +
          'A folytatáshoz nyisd meg a Profil menüt, és add meg a saját ingyenes Google AI Studio API kulcsodat (BYOK) a korlátlan beszélgetéshez!'
        );
        this.openModal('profileModal');
        return;
      }

      // Check if server or user has an API key configured
      if (this.settings && !this.settings.has_api_key && !this.currentUser.has_custom_key) {
        const entered = prompt('Kérlek add meg a Gemini API kulcsodat a hívás indításához:');
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

      // Reset mute & speaker buttons
      const muteBtn = document.getElementById('muteBtn');
      muteBtn.classList.remove('active');
      document.getElementById('muteLabel').textContent = 'Némítás';

      const isMobile = /Android|webOS|iPhone|iPad|iPod|BlackBerry|IEMobile|Opera Mini/i.test(navigator.userAgent) ||
        (navigator.userAgent.includes('Macintosh') && navigator.maxTouchPoints > 1);

      const speakerBtn = document.getElementById('speakerBtn');
      if (speakerBtn) {
        speakerBtn.classList.add('active');
        const speakerLabel = document.getElementById('speakerLabel');
        if (speakerLabel) speakerLabel.textContent = isMobile ? 'Kihangosító' : 'Hangszóró';
      }

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
        onAudioActivity: (rms) => {
          const orb = document.getElementById('pulseCoreOrb');
          const tip = document.getElementById('callBargeInTip');
          if (orb && this.currentCall && this.currentCall.state === 'listening') {
            if (rms > 0.015) {
              orb.style.boxShadow = '0 0 28px rgba(78, 124, 99, 0.7)';
              if (tip) tip.textContent = '🎙️ Hallom a hangodat! Befejezéskor mondd: "Your turn!", vagy koppints.';
            } else {
              orb.style.boxShadow = '';
            }
          }
        },
        onUserTranscript: (text) => {
          const tip = document.getElementById('callBargeInTip');
          if (tip && this.currentCall && this.currentCall.state === 'listening') {
            tip.textContent = `🗣️ "${text}"`;
          }
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
          // Refresh quota and user stats after call ends
          this.fetchMe();
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
          if (this.interruptModeTimeout) clearTimeout(this.interruptModeTimeout);
          this.setPassTurnButtonMode('thinking');
          break;

        case 'listening':
          statusText.textContent = 'Connected';
          stateLabel.textContent = 'Listening to you... (Beszélj bátran!)';
          bargeInTip.textContent = 'Mondd: "Your turn!", vagy koppints az átadáshoz.';
          if (this.interruptModeTimeout) clearTimeout(this.interruptModeTimeout);
          this.setPassTurnButtonMode('pass');
          break;

        case 'speaking':
          statusText.textContent = 'Connected';
          stateLabel.textContent = `${this.activePersona ? this.activePersona.name : 'Partner'} is speaking...`;
          bargeInTip.textContent = 'Sarah beszél... Koppints a félbeszakításhoz.';
          // Delay showing 'interrupt' button until 1.5s grace period passes
          if (this.interruptModeTimeout) clearTimeout(this.interruptModeTimeout);
          this.setPassTurnButtonMode('thinking');
          this.interruptModeTimeout = setTimeout(() => {
            if (this.currentCall && (this.currentCall.state === 'speaking' || this.currentCall.isAiSpeaking)) {
              this.setPassTurnButtonMode('interrupt');
            }
          }, 1500);
          break;

        case 'thinking':
          statusText.textContent = 'Connected';
          stateLabel.textContent = `${this.activePersona ? this.activePersona.name : 'Partner'} gondolkodik...`;
          bargeInTip.textContent = 'Válasz előkészítése... 🔔';
          if (this.interruptModeTimeout) clearTimeout(this.interruptModeTimeout);
          this.setPassTurnButtonMode('thinking');
          break;

        case 'ended':
          statusText.textContent = 'Call ended';
          stateLabel.textContent = 'Call completed';
          if (this.interruptModeTimeout) clearTimeout(this.interruptModeTimeout);
          this.setPassTurnButtonMode('pass');
          break;
      }
    },

    setPassTurnButtonMode(mode) {
      const btn = document.getElementById('passTurnBtn');
      const label = document.getElementById('passTurnLabel');
      const icon = document.getElementById('passTurnIcon');
      if (!btn) return;

      btn.classList.remove('primary-pass', 'interrupt-mode', 'thinking-mode');

      if (mode === 'interrupt') {
        btn.classList.add('interrupt-mode');
        btn.title = 'Félbeszakítás / Interrupt';
        if (label) label.textContent = 'Félbeszakítás';
        if (icon) {
          icon.innerHTML = `<rect x="6" y="6" width="12" height="12" rx="2" fill="currentColor"></rect>`;
        }
      } else if (mode === 'thinking') {
        btn.classList.add('thinking-mode');
        btn.title = 'Sarah válaszol... ⏳';
        if (label) label.textContent = 'Gondolkodik...';
        if (icon) {
          icon.innerHTML = `<svg class="spin-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><path d="M21 12a9 9 0 1 1-6.219-8.56"></path></svg>`;
        }
      } else {
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
      muteLabel.textContent = isMuted ? 'Mikrofon Be' : 'Némítás';
    },

    toggleCallSpeaker() {
      if (!this.currentCall) return;
      const isMobile = this.currentCall.isMobileDevice();
      if (!isMobile) {
        this.showCallToast('Asztali gépen a hangszóró / fejhallgató aktív 💻 (1.15x kényelmes hangerő)');
        return;
      }
      const isSpeaker = this.currentCall.toggleSpeakerphone();
      const speakerBtn = document.getElementById('speakerBtn');
      const speakerLabel = document.getElementById('speakerLabel');
      if (speakerBtn) {
        speakerBtn.classList.toggle('active', isSpeaker);
      }
      if (speakerLabel) {
        speakerLabel.textContent = isSpeaker ? 'Kihangosító' : 'Fülhallgató';
      }
      this.showCallToast(isSpeaker ? 'Kihangosító bekapcsolva 🔊 (Hangos)' : 'Fülhallgató / Kagyló mód 👂 (Privát)');
    },

    passCallTurn() {
      if (!this.currentCall) return;
      const callState = this.currentCall.state;
      const isSpeaking = this.currentCall.isAiSpeaking || callState === 'speaking';

      if (isSpeaking) {
        this.currentCall.manualInterrupt();
      } else if (callState === 'listening') {
        this.currentCall.passTurn();
      } else {
        console.log('[App] Pass/Interrupt click ignored during state:', callState);
      }
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
