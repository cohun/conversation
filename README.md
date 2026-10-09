# English Teacher & Conversational Companion

A voice-first, simulated telephone call web application pairing students with AI personas—led by Sarah, a patient, supportive English teacher implementing a structured 3-phase pedagogical cycle.

Built with **Go (Golang)**, **SQLite (pure Go / CGO-free)**, and **Google Gemini Multimodal Live API**.

---

## 🌟 Highlights & Key UX

- **100% Spoken Voice (Screen-text free during calls):** Calls feel like a genuine telephone conversation with no distracting text transcripts on-screen.
- **Pedagogical 3-Phase Teaching Protocol (Sarah):**
  1. *Phase 1 – Gentle Error Correction & Fluent Repetition:* Corrects grammatical/idiomatic slips and rephrases the sentence in natural, idiomatic English.
  2. *Phase 2 – Explanation & Practice Drill:* Succinctly explains why and offers a quick practice drill.
  3. *Phase 3 – Meaningful Dialogue & Follow-up:* Reacts substantively to the student's thought, shares perspective, and asks an engaging open-ended question.
- **Barge-in Support:** Speak anytime while the AI is talking to immediately silence output playback and seamlessly switch back to listening.
- **Turn-taking Flexibility:** Let natural silence conclude the turn, or tap the prominent *"Your Turn"* button.
- **Name & Pronunciation Handshake:** Custom phonetic pronunciation context so AI greets and refers to you with authentic stress and tone.
- **Save & Resume Memory:**
  - Say *"Let's save this topic for later"* or tap *"Bookmark"* to trigger the `save_conversation` tool.
  - Resume any saved topic with full context loaded into the opening prompt with a single tap.
- **Dynamic Model Resolver (Always-Optimal Resolution):** Auto-discovers available models supporting `bidiGenerateContent` with automatic fallback failover.
- **Ready for Google Cloud Run:** Includes a multi-stage Dockerfile, long-lived WebSocket support (`--timeout=3600`), and non-root runtime.

---

## 🏗️ Architecture

```
Conversation/
├── cmd/
│   └── server/
│       └── main.go           # HTTP server, routing, REST API handlers
├── internal/
│   ├── config/
│   │   └── config.go         # Environment and settings configuration
│   ├── db/
│   │   └── db.go             # Pure Go SQLite (modernc.org/sqlite), schema & seeds
│   ├── gemini/
│   │   ├── resolver.go       # Dynamic bidi model discovery & fallback chain
│   │   └── types.go          # Gemini Multimodal Live API protocol types
│   └── ws/
│       └── hub.go            # WebSocket proxy between browser & Gemini Live
├── web/
│   └── static/
│       ├── css/
│       │   └── style.css     # Soft pastel, responsive mobile-first UI & visualizer
│       ├── js/
│       │   ├── app.js        # Main frontend UI logic, tabs, modals, CRUD
│       │   ├── audio-call.js # Web Audio API, PCM downsampler & 24kHz playback
│       │   └── pcm-processor.js # AudioWorklet for 16kHz linear PCM recording
│       └── index.html        # Clean HTML5 mobile-optimized interface
├── data/                     # SQLite database storage (app.db)
├── Dockerfile                # Multi-stage Cloud Run container
├── deploy.sh                 # Cloud Run deployment script
├── go.mod
└── go.sum
```

---

## 🚀 Quickstart

### Prerequisites
- Go 1.22+ installed
- Google Gemini API Key

### Running Locally

1. Set your Gemini API key (optional if setting in UI):
   ```bash
   # Windows PowerShell
   $env:GEMINI_API_KEY="your-api-key-here"

   # Linux/macOS
   export GEMINI_API_KEY="your-api-key-here"
   ```

2. Build and run:
   ```bash
   go run ./cmd/server
   ```

3. Open your browser:
   ```
   http://localhost:8080
   ```

---

## ☁️ Deployment to Google Cloud Run

```bash
chmod +x deploy.sh
./deploy.sh
```

Or via direct `gcloud` command:
```bash
gcloud run deploy english-teacher-app \
  --source . \
  --platform managed \
  --region europe-west1 \
  --allow-unauthenticated \
  --timeout 3600 \
  --set-env-vars GEMINI_API_KEY="YOUR_KEY",GEMINI_LIVE_MODEL="auto",DB_PATH="/data/app.db"
```
