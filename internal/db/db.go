package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
	mu   sync.RWMutex
}

type UserProfile struct {
	ID           int64  `json:"id"`
	UserID       string `json:"user_id"`
	DisplayName  string `json:"display_name"`
	PhoneticNote string `json:"phonetic_note"`
	CreatedAt    string `json:"created_at"`
}

type Persona struct {
	ID           string `json:"id"`
	UserID       string `json:"user_id"`
	Name         string `json:"name"`
	ShortDesc    string `json:"short_desc"`
	SystemPrompt string `json:"system_prompt"`
	VoiceName    string `json:"voice_name"`
	Mode         string `json:"mode"`
	IsDefault    bool   `json:"is_default"`
	AvatarIcon   string `json:"avatar_icon"`
	CreatedAt    string `json:"created_at"`
	HasOverride  bool   `json:"has_override"`
}

type SavedConversation struct {
	ID             string  `json:"id"`
	UserID         string  `json:"user_id"`
	PersonaID      string  `json:"persona_id"`
	PersonaName    string  `json:"persona_name,omitempty"`
	TopicTitle     string  `json:"topic_title"`
	Summary        string  `json:"summary"`
	KeyPoints      string  `json:"key_points"`
	OpenQuestion   string  `json:"open_question"`
	CreatedAt      string  `json:"created_at"`
	LastResumedAt  *string `json:"last_resumed_at"`
}

func initDBSingle(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory %s: %w", dir, err)
	}

	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(DELETE)"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	database := &DB{conn: conn}
	if err := database.migrate(); err != nil {
		conn.Close()
		// Auto-recovery for corrupted SQLite files (e.g. malformed disk image after hard kill or container sync issue)
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "malformed") ||
			strings.Contains(errStr, "corrupt") ||
			strings.Contains(errStr, "not a database") ||
			strings.Contains(errStr, "disk i/o error") {
			log.Printf("[DB] ⚠️ Corrupted SQLite database detected (%v). Deleting corrupted files at %s...", err, dbPath)
			_ = os.Remove(dbPath)
			_ = os.Remove(dbPath + "-wal")
			_ = os.Remove(dbPath + "-shm")
			_ = os.Remove(dbPath + "-journal")

			// Re-open clean database
			conn, err = sql.Open("sqlite", dsn)
			if err != nil {
				return nil, fmt.Errorf("failed to reopen sqlite database after corruption: %w", err)
			}
			database.conn = conn
			if err := database.migrate(); err != nil {
				conn.Close()
				return nil, fmt.Errorf("failed to migrate fresh database: %w", err)
			}
			log.Printf("[DB] ✅ Successfully recovered fresh SQLite database at %s.", dbPath)
		} else {
			return nil, fmt.Errorf("failed to migrate database: %w", err)
		}
	}

	if err := database.seed(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to seed database: %w", err)
	}

	return database, nil
}

func InitDB(dbPath string) (*DB, error) {
	database, err := initDBSingle(dbPath)
	if err == nil {
		return database, nil
	}

	log.Printf("[DB] ⚠️ Failed to initialize database at %s: %v. Falling back to /tmp/app.db...", dbPath, err)
	tmpPath := "/tmp/app.db"
	if dbPath != tmpPath {
		_ = os.Remove(tmpPath)
		_ = os.Remove(tmpPath + "-wal")
		_ = os.Remove(tmpPath + "-shm")
		_ = os.Remove(tmpPath + "-journal")

		tmpDB, tmpErr := initDBSingle(tmpPath)
		if tmpErr == nil {
			log.Printf("[DB] ✅ Successfully initialized resilient fallback database at %s", tmpPath)
			return tmpDB, nil
		}
		log.Printf("[DB] ⚠️ Fallback to %s also failed: %v", tmpPath, tmpErr)
	}

	return nil, err
}

func (d *DB) Close() error {
	return d.conn.Close()
}

func (d *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS user_profile (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id TEXT NOT NULL DEFAULT 'default_user',
		display_name TEXT NOT NULL,
		phonetic_note TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS personas (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL DEFAULT 'default_user',
		name TEXT NOT NULL,
		short_desc TEXT NOT NULL,
		system_prompt TEXT NOT NULL,
		voice_name TEXT NOT NULL,
		mode TEXT NOT NULL CHECK(mode IN ('teacher', 'free_chat')),
		is_default BOOLEAN DEFAULT 0,
		avatar_icon TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS saved_conversations (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL DEFAULT 'default_user',
		persona_id TEXT NOT NULL,
		topic_title TEXT NOT NULL,
		summary TEXT NOT NULL,
		key_points TEXT,
		open_question TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_resumed_at DATETIME,
		FOREIGN KEY(persona_id) REFERENCES personas(id)
	);

	CREATE TABLE IF NOT EXISTS app_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		email TEXT UNIQUE NOT NULL,
		password_hash TEXT,
		auth_provider TEXT NOT NULL DEFAULT 'local',
		google_sub TEXT,
		role TEXT NOT NULL DEFAULT 'student',
		status TEXT NOT NULL DEFAULT 'pending',
		daily_limit_minutes INTEGER NOT NULL DEFAULT 45,
		custom_api_key TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS sessions (
		token TEXT PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		expires_at DATETIME NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS call_logs (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		persona_id TEXT NOT NULL,
		started_at DATETIME NOT NULL,
		ended_at DATETIME NOT NULL,
		duration_seconds INTEGER NOT NULL DEFAULT 0,
		used_custom_key BOOLEAN NOT NULL DEFAULT 0,
		estimated_cost_usd REAL NOT NULL DEFAULT 0.0
	);

	CREATE TABLE IF NOT EXISTS user_persona_overrides (
		user_id TEXT NOT NULL,
		persona_id TEXT NOT NULL,
		name TEXT NOT NULL,
		short_desc TEXT NOT NULL,
		system_prompt TEXT NOT NULL,
		voice_name TEXT NOT NULL,
		mode TEXT NOT NULL CHECK(mode IN ('teacher', 'free_chat')),
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (user_id, persona_id)
	);

	CREATE INDEX IF NOT EXISTS idx_personas_user_id ON personas(user_id);
	CREATE INDEX IF NOT EXISTS idx_persona_overrides_user ON user_persona_overrides(user_id);
	CREATE INDEX IF NOT EXISTS idx_saved_conv_user_id ON saved_conversations(user_id);
	CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
	CREATE INDEX IF NOT EXISTS idx_call_logs_user_id ON call_logs(user_id);
	`
	_, err := d.conn.Exec(schema)
	return err
}

func (d *DB) seed() error {
	// Seed app_settings if not present
	_, err := d.conn.Exec(`
		INSERT OR IGNORE INTO app_settings (key, value) VALUES ('default_live_model', 'auto');
	`)
	if err != nil {
		return err
	}

	// Seed user_profile if empty
	var count int
	err = d.conn.QueryRow(`SELECT COUNT(*) FROM user_profile`).Scan(&count)
	if err != nil {
		return err
	}
	if count == 0 {
		_, err = d.conn.Exec(`
			INSERT INTO user_profile (user_id, display_name, phonetic_note) 
			VALUES ('default_user', 'Alex', 'Alex (pronounced AH-leks, stress on first syllable)');
		`)
		if err != nil {
			return err
		}
	}

	// Seed default personas if empty
	err = d.conn.QueryRow(`SELECT COUNT(*) FROM personas`).Scan(&count)
	if err != nil {
		return err
	}
	if count == 0 {
		sarahPrompt := `You are Sarah, a warm, patient, and exceptionally encouraging English teacher having an interactive phone call with your student. You communicate strictly via natural spoken audio.

Your mission is to help the student build confidence, fluency, and idiomatic mastery in spoken English through relaxed, natural conversation.

Whenever the student speaks, you MUST adhere to this pedagogical 3-phase cycle in your response turn:

Phase 1 - Error Correction & Natural Repetition:
Gently and briefly point out any grammatical, lexical, prepositional, or pronunciation inaccuracies in what the student said.
Then, repeat the student's idea or sentence using clean, perfect, and natural English. If the student made no errors, praise their phrasing briefly.

Phase 2 - Explanation & Practice Drill Offer:
Provide a quick, intuitive 1-sentence explanation of why the correction is needed.
Briefly offer a quick drill or practice opportunity (e.g., "Would you like to try saying that sentence again?" or "Try saying it after me: ...").
If the student chooses to practice the drill, celebrate their effort and give feedback. If the student continues the discussion or says they want to move on, immediately advance to Phase 3.

Phase 3 - Conversational Engagement & Open Dialogue:
Respond thoughtfully and engagingly to the substance of the student's thought or story. Share your own perspective or experience, and close your turn with an engaging open-ended question that naturally prompts the student to speak next.

Guidelines for Spoken Phone Call Experience:
- Keep each spoken turn conversational and concise (typically 2-4 sentences total across all 3 phases). Avoid long speeches.
- Speak in a warm, patient, friendly tone at a natural, comfortable pace.
- Do not mention formatting, asterisks, bullet points, or visual cues, as this is a voice-only phone call.
- Always address the student respectfully by their name and pronunciation notes as provided in context.

Conversation Bookmarking:
You have access to the 'save_conversation' tool. Whenever the student says "let's save this topic", "bookmark this", "save our conversation", or indicates they want to save the topic for later, invoke the 'save_conversation' tool with topic_title, summary, key_points, and open_question.
After invoking the tool, verbally confirm: "I've saved our conversation about [topic_title]. We can pick it up anytime!"`

		fredPrompt := `You are Fred, a friendly, witty, and easygoing conversation partner on a phone call with the user.
Your role is Free Chat: you are an equal conversation buddy, NOT an instructor or teacher.
Do NOT correct grammar or give language lessons unless explicitly asked.
Speak casually, naturally, and warmly. Use contemporary idioms, share thoughts on daily life, hobbies, travel, movies, tech, and cultural experiences.
Keep your responses conversational and bite-sized (1-3 sentences), like a real telephone call with a close friend.
End your turns with natural questions or banter.
If the user asks to save or bookmark the topic, call the 'save_conversation' tool and confirm verbally.`

		mayaPrompt := `You are Maya, an executive business English coach having a targeted coaching call with the user.
You help your student sound professional, articulate, and persuasive in workplace situations, presentations, and interviews.
Follow the 3-phase pedagogical protocol:
Phase 1: Identify informal, vague, or non-native phrasing and rephrase it into crisp, executive-level business English.
Phase 2: Briefly explain the nuance and offer a quick practice drill.
Phase 3: Substantively address their business idea and ask a strategic follow-up question.
Keep your tone polished, warm, and empowering. If the user asks to save the topic, invoke the 'save_conversation' tool.`

		_, err = d.conn.Exec(`
			INSERT INTO personas (id, user_id, name, short_desc, system_prompt, voice_name, mode, is_default, avatar_icon)
			VALUES 
			('teacher-sarah', 'default_user', 'Sarah', 'Patient, supportive English teacher focused on gentle corrections and natural dialogue.', ?, 'Aoede', 'teacher', 1, 'sarah'),
			('friend-fred', 'default_user', 'Fred', 'Easygoing conversation buddy for casual chats about daily life, travel, movies, and hobbies.', ?, 'Puck', 'free_chat', 1, 'fred'),
			('coach-maya', 'default_user', 'Maya', 'Executive business English and interview coach focusing on professional clarity and vocabulary.', ?, 'Kore', 'teacher', 1, 'maya');
		`, sarahPrompt, fredPrompt, mayaPrompt)
		if err != nil {
			return err
		}
	}

	// Automatic migration: ensure all 3 core companions (Sarah, Fred, Maya) are always present with canonical default settings
	sarahPrompt := `You are Sarah, a warm, patient, and exceptionally encouraging English teacher having an interactive phone call with your student. You communicate strictly via natural spoken audio.

Your mission is to help the student build confidence, fluency, and idiomatic mastery in spoken English through relaxed, natural conversation.

Whenever the student speaks, you MUST adhere to this pedagogical 3-phase cycle in your response turn:

Phase 1 - Error Correction & Natural Repetition:
Gently and briefly point out any grammatical, lexical, prepositional, or pronunciation inaccuracies in what the student said.
Then, repeat the student's idea or sentence using clean, perfect, and natural English. If the student made no errors, praise their phrasing briefly.

Phase 2 - Explanation & Practice Drill Offer:
Provide a quick, intuitive 1-sentence explanation of why the correction is needed.
Briefly offer a quick drill or practice opportunity (e.g., "Would you like to try saying that sentence again?" or "Try saying it after me: ...").
If the student chooses to practice the drill, celebrate their effort and give feedback. If the student continues the discussion or says they want to move on, immediately advance to Phase 3.

Phase 3 - Conversational Engagement & Open Dialogue:
Respond thoughtfully and engagingly to the substance of the student's thought or story. Share your own perspective or experience, and close your turn with an engaging open-ended question that naturally prompts the student to speak next.

Guidelines for Spoken Phone Call Experience:
- Keep each spoken turn conversational and concise (typically 2-4 sentences total across all 3 phases). Avoid long speeches.
- Speak in a warm, patient, friendly tone at a natural, comfortable pace.
- Do not mention formatting, asterisks, bullet points, or visual cues, as this is a voice-only phone call.
- Always address the student respectfully by their name and pronunciation notes as provided in context.

Conversation Bookmarking:
You have access to the 'save_conversation' tool. Whenever the student says "let's save this topic", "bookmark this", "save our conversation", or indicates they want to save the topic for later, invoke the 'save_conversation' tool with topic_title, summary, key_points, and open_question.
After invoking the tool, verbally confirm: "I've saved our conversation about [topic_title]. We can pick it up anytime!"`

	fredPrompt := `You are Fred, a friendly, witty, and easygoing conversation partner on a phone call with the user.
Your role is Free Chat: you are an equal conversation buddy, NOT an instructor or teacher.
Do NOT correct grammar or give language lessons unless explicitly asked.
Speak casually, naturally, and warmly. Use contemporary idioms, share thoughts on daily life, hobbies, travel, movies, tech, and cultural experiences.
Keep your responses conversational and bite-sized (1-3 sentences), like a real telephone call with a close friend.
End your turns with natural questions or banter.
If the user asks to save or bookmark the topic, call the 'save_conversation' tool and confirm verbally.`

	mayaPrompt := `You are Maya, an executive business English coach having a targeted coaching call with the user.
You help your student sound professional, articulate, and persuasive in workplace situations, presentations, and interviews.
Follow the 3-phase pedagogical protocol:
Phase 1: Identify informal, vague, or non-native phrasing and rephrase it into crisp, executive-level business English.
Phase 2: Briefly explain the nuance and offer a quick practice drill.
Phase 3: Substantively address their business idea and ask a strategic follow-up question.
Keep your tone polished, warm, and empowering. If the user asks to save the topic, invoke the 'save_conversation' tool.`

	_, _ = d.conn.Exec(`
		INSERT OR IGNORE INTO personas (id, user_id, name, short_desc, system_prompt, voice_name, mode, is_default, avatar_icon)
		VALUES 
		('teacher-sarah', 'default_user', 'Sarah', 'Patient, supportive English teacher focused on gentle corrections and natural dialogue.', ?, 'Aoede', 'teacher', 1, 'sarah'),
		('friend-fred', 'default_user', 'Fred', 'Easygoing conversation buddy for casual chats about daily life, travel, movies, and hobbies.', ?, 'Puck', 'free_chat', 1, 'fred'),
		('coach-maya', 'default_user', 'Maya', 'Executive business English and interview coach focusing on professional clarity and vocabulary.', ?, 'Kore', 'teacher', 1, 'maya');
	`, sarahPrompt, fredPrompt, mayaPrompt)

	// Automatic migration: rename legacy 'Leo' to 'Fred' in any existing databases
	_, _ = d.conn.Exec(`
		UPDATE personas 
		SET id = 'friend-fred', 
		    name = 'Fred', 
		    avatar_icon = 'fred', 
		    system_prompt = REPLACE(system_prompt, 'You are Leo', 'You are Fred') 
		WHERE id = 'friend-leo' OR name = 'Leo';
	`)

	// Automatic migration: restore clean canonical default templates in personas table
	_, _ = d.conn.Exec(`
		UPDATE personas 
		SET name = 'Sarah',
		    short_desc = 'Patient, supportive English teacher focused on gentle corrections and natural dialogue.',
		    system_prompt = ?,
		    voice_name = 'Aoede',
		    mode = 'teacher',
		    is_default = 1,
		    avatar_icon = 'sarah'
		WHERE id = 'teacher-sarah';
	`, sarahPrompt)

	_, _ = d.conn.Exec(`
		UPDATE personas 
		SET name = 'Fred',
		    short_desc = 'Easygoing conversation buddy for casual chats about daily life, travel, movies, and hobbies.',
		    system_prompt = ?,
		    voice_name = 'Puck',
		    mode = 'free_chat',
		    is_default = 1,
		    avatar_icon = 'fred'
		WHERE id = 'friend-fred';
	`, fredPrompt)

	_, _ = d.conn.Exec(`
		UPDATE personas 
		SET name = 'Maya',
		    short_desc = 'Executive business English and interview coach focusing on professional clarity and vocabulary.',
		    system_prompt = ?,
		    voice_name = 'Kore',
		    mode = 'teacher',
		    is_default = 1,
		    avatar_icon = 'maya'
		WHERE id = 'coach-maya';
	`, mayaPrompt)

	return nil
}

// User Profile
func (d *DB) GetUserProfile(userID string) (*UserProfile, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var p UserProfile
	err := d.conn.QueryRow(`
		SELECT id, user_id, display_name, COALESCE(phonetic_note, ''), created_at 
		FROM user_profile 
		WHERE user_id = ? 
		ORDER BY id DESC LIMIT 1
	`, userID).Scan(&p.ID, &p.UserID, &p.DisplayName, &p.PhoneticNote, &p.CreatedAt)
	if err == sql.ErrNoRows || (p.DisplayName == "Alex" && userID != "default_user") {
		var email string
		_ = d.conn.QueryRow(`SELECT email FROM users WHERE id = ?`, userID).Scan(&email)
		displayName := "Attila"
		if email != "" {
			parts := strings.Split(email, "@")
			clean := parts[0]
			clean = strings.ReplaceAll(clean, ".", " ")
			clean = strings.ReplaceAll(clean, "_", " ")
			clean = strings.ReplaceAll(clean, "-", " ")
			displayName = strings.Title(strings.TrimSpace(clean))
		}
		if p.ID == 0 {
			return &UserProfile{
				UserID:       userID,
				DisplayName:  displayName,
				PhoneticNote: fmt.Sprintf("%s (pronounced %s)", displayName, displayName),
			}, nil
		}
		p.DisplayName = displayName
		return &p, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (d *DB) UpdateUserProfile(userID, displayName, phoneticNote string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var count int
	err := d.conn.QueryRow(`SELECT COUNT(*) FROM user_profile WHERE user_id = ?`, userID).Scan(&count)
	if err != nil {
		return err
	}

	if count == 0 {
		_, err = d.conn.Exec(`
			INSERT INTO user_profile (user_id, display_name, phonetic_note) 
			VALUES (?, ?, ?)
		`, userID, displayName, phoneticNote)
	} else {
		_, err = d.conn.Exec(`
			UPDATE user_profile 
			SET display_name = ?, phonetic_note = ? 
			WHERE user_id = ?
		`, displayName, phoneticNote, userID)
	}
	return err
}

// Personas
func (d *DB) ListPersonas(userID string) ([]Persona, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.conn.Query(`
		SELECT 
			p.id, 
			p.user_id, 
			COALESCE(o.name, p.name) AS name, 
			COALESCE(o.short_desc, p.short_desc) AS short_desc, 
			COALESCE(o.system_prompt, p.system_prompt) AS system_prompt, 
			COALESCE(o.voice_name, p.voice_name) AS voice_name, 
			COALESCE(o.mode, p.mode) AS mode, 
			p.is_default, 
			COALESCE(p.avatar_icon, ''), 
			p.created_at,
			CASE WHEN o.persona_id IS NOT NULL THEN 1 ELSE 0 END AS has_override
		FROM personas p
		LEFT JOIN user_persona_overrides o ON p.id = o.persona_id AND o.user_id = ?
		WHERE p.is_default = 1 OR p.user_id = ?
		ORDER BY p.is_default DESC, p.created_at ASC
	`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Persona{}
	for rows.Next() {
		var p Persona
		var hasOverride int
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.ShortDesc, &p.SystemPrompt, &p.VoiceName, &p.Mode, &p.IsDefault, &p.AvatarIcon, &p.CreatedAt, &hasOverride); err != nil {
			return nil, err
		}
		p.HasOverride = (hasOverride == 1)
		list = append(list, p)
	}
	return list, rows.Err()
}

func (d *DB) GetPersona(id string) (*Persona, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var p Persona
	err := d.conn.QueryRow(`
		SELECT id, user_id, name, short_desc, system_prompt, voice_name, mode, is_default, COALESCE(avatar_icon, ''), created_at
		FROM personas
		WHERE id = ?
	`, id).Scan(&p.ID, &p.UserID, &p.Name, &p.ShortDesc, &p.SystemPrompt, &p.VoiceName, &p.Mode, &p.IsDefault, &p.AvatarIcon, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (d *DB) GetPersonaForUser(personaID, userID string) (*Persona, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var p Persona
	var hasOverride int
	err := d.conn.QueryRow(`
		SELECT 
			p.id, 
			p.user_id, 
			COALESCE(o.name, p.name) AS name, 
			COALESCE(o.short_desc, p.short_desc) AS short_desc, 
			COALESCE(o.system_prompt, p.system_prompt) AS system_prompt, 
			COALESCE(o.voice_name, p.voice_name) AS voice_name, 
			COALESCE(o.mode, p.mode) AS mode, 
			p.is_default, 
			COALESCE(p.avatar_icon, ''), 
			p.created_at,
			CASE WHEN o.persona_id IS NOT NULL THEN 1 ELSE 0 END AS has_override
		FROM personas p
		LEFT JOIN user_persona_overrides o ON p.id = o.persona_id AND o.user_id = ?
		WHERE p.id = ?
	`, userID, personaID).Scan(&p.ID, &p.UserID, &p.Name, &p.ShortDesc, &p.SystemPrompt, &p.VoiceName, &p.Mode, &p.IsDefault, &p.AvatarIcon, &p.CreatedAt, &hasOverride)
	if err != nil {
		return nil, err
	}
	p.HasOverride = (hasOverride == 1)
	return &p, nil
}

func (d *DB) SaveUserPersonaOverride(userID, personaID, name, shortDesc, systemPrompt, voiceName, mode string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`
		INSERT INTO user_persona_overrides (user_id, persona_id, name, short_desc, system_prompt, voice_name, mode, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id, persona_id) DO UPDATE SET
			name = excluded.name,
			short_desc = excluded.short_desc,
			system_prompt = excluded.system_prompt,
			voice_name = excluded.voice_name,
			mode = excluded.mode,
			updated_at = CURRENT_TIMESTAMP
	`, userID, personaID, name, shortDesc, systemPrompt, voiceName, mode)
	return err
}

func (d *DB) ResetUserPersonaOverride(userID, personaID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`DELETE FROM user_persona_overrides WHERE user_id = ? AND persona_id = ?`, userID, personaID)
	return err
}

func (d *DB) GetDefaultPersonaTemplate(personaID string) (*Persona, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var p Persona
	err := d.conn.QueryRow(`
		SELECT id, user_id, name, short_desc, system_prompt, voice_name, mode, is_default, COALESCE(avatar_icon, ''), created_at
		FROM personas
		WHERE id = ? AND is_default = 1
	`, personaID).Scan(&p.ID, &p.UserID, &p.Name, &p.ShortDesc, &p.SystemPrompt, &p.VoiceName, &p.Mode, &p.IsDefault, &p.AvatarIcon, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	p.HasOverride = false
	return &p, nil
}

func (d *DB) CreatePersona(p *Persona) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if p.ID == "" {
		p.ID = "custom-" + uuid.New().String()[:8]
	}
	if p.UserID == "" {
		p.UserID = "default_user"
	}

	_, err := d.conn.Exec(`
		INSERT INTO personas (id, user_id, name, short_desc, system_prompt, voice_name, mode, is_default, avatar_icon)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, p.ID, p.UserID, p.Name, p.ShortDesc, p.SystemPrompt, p.VoiceName, p.Mode, p.IsDefault, p.AvatarIcon)
	return err
}

func (d *DB) UpdatePersona(p *Persona) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`
		UPDATE personas 
		SET name = ?, short_desc = ?, system_prompt = ?, voice_name = ?, mode = ?
		WHERE id = ?
	`, p.Name, p.ShortDesc, p.SystemPrompt, p.VoiceName, p.Mode, p.ID)
	return err
}

func (d *DB) DeletePersona(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`DELETE FROM personas WHERE id = ? AND is_default = 0`, id)
	return err
}

// Saved Conversations
func (d *DB) ListConversations(userID string) ([]SavedConversation, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.conn.Query(`
		SELECT s.id, s.user_id, s.persona_id, COALESCE(p.name, 'Unknown'), s.topic_title, s.summary, 
		       COALESCE(s.key_points, ''), COALESCE(s.open_question, ''), s.created_at, s.last_resumed_at
		FROM saved_conversations s
		LEFT JOIN personas p ON s.persona_id = p.id
		WHERE s.user_id = ?
		ORDER BY COALESCE(s.last_resumed_at, s.created_at) DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []SavedConversation{}
	for rows.Next() {
		var c SavedConversation
		if err := rows.Scan(&c.ID, &c.UserID, &c.PersonaID, &c.PersonaName, &c.TopicTitle, &c.Summary, &c.KeyPoints, &c.OpenQuestion, &c.CreatedAt, &c.LastResumedAt); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

func (d *DB) GetConversation(id string) (*SavedConversation, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var c SavedConversation
	err := d.conn.QueryRow(`
		SELECT s.id, s.user_id, s.persona_id, COALESCE(p.name, 'Unknown'), s.topic_title, s.summary, 
		       COALESCE(s.key_points, ''), COALESCE(s.open_question, ''), s.created_at, s.last_resumed_at
		FROM saved_conversations s
		LEFT JOIN personas p ON s.persona_id = p.id
		WHERE s.id = ?
	`, id).Scan(&c.ID, &c.UserID, &c.PersonaID, &c.PersonaName, &c.TopicTitle, &c.Summary, &c.KeyPoints, &c.OpenQuestion, &c.CreatedAt, &c.LastResumedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (d *DB) SaveConversation(conv *SavedConversation) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if conv.ID == "" {
		conv.ID = "topic-" + uuid.New().String()[:8]
	}
	if conv.UserID == "" {
		conv.UserID = "default_user"
	}

	_, err := d.conn.Exec(`
		INSERT INTO saved_conversations (id, user_id, persona_id, topic_title, summary, key_points, open_question)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, conv.ID, conv.UserID, conv.PersonaID, conv.TopicTitle, conv.Summary, conv.KeyPoints, conv.OpenQuestion)
	return err
}

func (d *DB) UpdateConversationResumed(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now().Format("2006-01-02 15:04:05")
	_, err := d.conn.Exec(`
		UPDATE saved_conversations
		SET last_resumed_at = ?
		WHERE id = ?
	`, now, id)
	return err
}

func (d *DB) DeleteConversation(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`DELETE FROM saved_conversations WHERE id = ?`, id)
	return err
}

// App Settings
func (d *DB) GetSetting(key string) (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var val string
	err := d.conn.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

func (d *DB) SetSetting(key, value string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`
		INSERT INTO app_settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}

func (d *DB) GetAllSettings() (map[string]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.conn.Query(`SELECT key, value FROM app_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		settings[k] = v
	}
	return settings, rows.Err()
}
