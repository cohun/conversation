package db

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID                string `json:"id"`
	Email             string `json:"email"`
	PasswordHash      string `json:"-"`
	AuthProvider      string `json:"auth_provider"` // "google", "local"
	GoogleSub         string `json:"google_sub,omitempty"`
	Role              string `json:"role"`   // "admin", "student"
	Status            string `json:"status"` // "active", "pending", "blocked"
	DailyLimitMinutes int    `json:"daily_limit_minutes"`
	CustomAPIKey      string `json:"custom_api_key,omitempty"`
	CreatedAt         string `json:"created_at"`
}

type Session struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt string    `json:"created_at"`
}

type CallLog struct {
	ID               string    `json:"id"`
	UserID           string    `json:"user_id"`
	PersonaID        string    `json:"persona_id"`
	StartedAt        time.Time `json:"started_at"`
	EndedAt          time.Time `json:"ended_at"`
	DurationSeconds  int       `json:"duration_seconds"`
	UsedCustomKey    bool      `json:"used_custom_key"`
	EstimatedCostUSD float64   `json:"estimated_cost_usd"`
}

type AdminUserView struct {
	ID                string  `json:"id"`
	Email             string  `json:"email"`
	DisplayName       string  `json:"display_name"`
	AuthProvider      string  `json:"auth_provider"`
	Role              string  `json:"role"`
	Status            string  `json:"status"`
	DailyLimitMinutes int     `json:"daily_limit_minutes"`
	HasCustomKey      bool    `json:"has_custom_key"`
	MaskedCustomKey   string  `json:"masked_custom_key"`
	TotalCalls        int     `json:"total_calls"`
	TotalDurationSec  int     `json:"total_duration_sec"`
	TodayUsedMinutes  int     `json:"today_used_minutes"`
	TotalCostUSD      float64 `json:"total_cost_usd"`
	LastActive        string  `json:"last_active"`
	CreatedAt         string  `json:"created_at"`
}

func maskAPIKey(k string) string {
	k = strings.TrimSpace(k)
	if k == "" {
		return ""
	}
	if len(k) <= 8 {
		return "***"
	}
	return k[:4] + "..." + k[len(k)-4:]
}

func (d *DB) CreateUser(
	email string,
	passwordHash string,
	authProvider string,
	googleSub string,
	role string,
	status string,
	displayName string,
) (*User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, errors.New("email is required")
	}

	userID := "usr_" + uuid.New().String()[:12]
	if role == "" {
		role = "student"
	}
	if status == "" {
		status = "pending"
	}
	if authProvider == "" {
		authProvider = "local"
	}

	dailyLimit := 45 // 45 minutes default daily practice quota

	_, err := d.conn.Exec(`
		INSERT INTO users (id, email, password_hash, auth_provider, google_sub, role, status, daily_limit_minutes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, userID, email, passwordHash, authProvider, googleSub, role, status, dailyLimit)
	if err != nil {
		return nil, err
	}

	if displayName == "" {
		// Clean email prefix into a friendly display name (replace dots, underscores, hyphens with spaces)
		parts := strings.Split(email, "@")
		clean := parts[0]
		clean = strings.ReplaceAll(clean, ".", " ")
		clean = strings.ReplaceAll(clean, "_", " ")
		clean = strings.ReplaceAll(clean, "-", " ")
		displayName = strings.Title(strings.TrimSpace(clean))
	}

	// Create user profile
	defaultPhonetic := fmt.Sprintf("%s (pronounced %s)", displayName, displayName)
	if strings.Contains(strings.ToLower(email), "attila") {
		displayName = "Attila"
		defaultPhonetic = "Attila (pronounced AH-teel-ah)"
	}
	_, err = d.conn.Exec(`
		INSERT INTO user_profile (user_id, display_name, phonetic_note)
		VALUES (?, ?, ?)
	`, userID, displayName, defaultPhonetic)
	if err != nil {
		return nil, err
	}

	return &User{
		ID:                userID,
		Email:             email,
		AuthProvider:      authProvider,
		GoogleSub:         googleSub,
		Role:              role,
		Status:            status,
		DailyLimitMinutes: dailyLimit,
	}, nil
}

func (d *DB) GetUserByEmail(email string) (*User, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	email = strings.ToLower(strings.TrimSpace(email))
	var u User
	var pwHash sql.NullString
	var gSub sql.NullString
	var customKey sql.NullString

	err := d.conn.QueryRow(`
		SELECT id, email, password_hash, auth_provider, google_sub, role, status, daily_limit_minutes, custom_api_key, created_at
		FROM users
		WHERE email = ?
	`, email).Scan(&u.ID, &u.Email, &pwHash, &u.AuthProvider, &gSub, &u.Role, &u.Status, &u.DailyLimitMinutes, &customKey, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if pwHash.Valid {
		u.PasswordHash = pwHash.String
	}
	if gSub.Valid {
		u.GoogleSub = gSub.String
	}
	if customKey.Valid {
		u.CustomAPIKey = customKey.String
	}
	return &u, nil
}

func (d *DB) GetUserByID(id string) (*User, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var u User
	var pwHash sql.NullString
	var gSub sql.NullString
	var customKey sql.NullString

	err := d.conn.QueryRow(`
		SELECT id, email, password_hash, auth_provider, google_sub, role, status, daily_limit_minutes, custom_api_key, created_at
		FROM users
		WHERE id = ?
	`, id).Scan(&u.ID, &u.Email, &pwHash, &u.AuthProvider, &gSub, &u.Role, &u.Status, &u.DailyLimitMinutes, &customKey, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if pwHash.Valid {
		u.PasswordHash = pwHash.String
	}
	if gSub.Valid {
		u.GoogleSub = gSub.String
	}
	if customKey.Valid {
		u.CustomAPIKey = customKey.String
	}
	return &u, nil
}

func (d *DB) UpdateUserGoogleSub(userID, googleSub string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`UPDATE users SET google_sub = ? WHERE id = ?`, googleSub, userID)
	return err
}

func (d *DB) UpdateUserStatus(userID, status string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`UPDATE users SET status = ? WHERE id = ?`, status, userID)
	return err
}

func (d *DB) UpdateUserRole(userID, role string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`UPDATE users SET role = ? WHERE id = ?`, role, userID)
	return err
}

func (d *DB) UpdateUserDailyLimit(userID string, limitMinutes int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if limitMinutes < 5 {
		limitMinutes = 5
	}
	_, err := d.conn.Exec(`UPDATE users SET daily_limit_minutes = ? WHERE id = ?`, limitMinutes, userID)
	return err
}

func (d *DB) UpdateUserCustomKey(userID, customKey string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`UPDATE users SET custom_api_key = ? WHERE id = ?`, strings.TrimSpace(customKey), userID)
	return err
}

func (d *DB) UpdateUserPassword(userID, passwordHash string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, userID)
	return err
}

func (d *DB) DeleteUser(userID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`DELETE FROM users WHERE id = ?`, userID)
	return err
}

// Session Management
func (d *DB) CreateSession(userID string, duration time.Duration) (*Session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(bytes)
	expiresAt := time.Now().Add(duration)

	_, err := d.conn.Exec(`
		INSERT INTO sessions (token, user_id, expires_at)
		VALUES (?, ?, ?)
	`, token, userID, expiresAt)
	if err != nil {
		return nil, err
	}

	return &Session{
		Token:     token,
		UserID:    userID,
		ExpiresAt: expiresAt,
	}, nil
}

func (d *DB) CreateSessionWithToken(token, userID string, duration time.Duration) (*Session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	expiresAt := time.Now().Add(duration)
	_, err := d.conn.Exec(`
		INSERT OR REPLACE INTO sessions (token, user_id, expires_at)
		VALUES (?, ?, ?)
	`, token, userID, expiresAt)
	if err != nil {
		return nil, err
	}

	return &Session{
		Token:     token,
		UserID:    userID,
		ExpiresAt: expiresAt,
	}, nil
}

func (d *DB) GetSession(token string) (*Session, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	token = strings.TrimSpace(token)
	if token == "" {
		return nil, nil
	}

	var s Session
	err := d.conn.QueryRow(`
		SELECT token, user_id, expires_at, created_at
		FROM sessions
		WHERE token = ? AND expires_at > CURRENT_TIMESTAMP
	`, token).Scan(&s.Token, &s.UserID, &s.ExpiresAt, &s.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (d *DB) DeleteSession(token string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

func (d *DB) CleanExpiredSessions() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.conn.Exec(`DELETE FROM sessions WHERE expires_at <= CURRENT_TIMESTAMP`)
	return err
}

// Call Logs and Quota
func (d *DB) LogCall(call *CallLog) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if call.ID == "" {
		call.ID = "call_" + uuid.New().String()[:12]
	}

	// Cost formula estimate: ~$0.012 per minute on Gemini Live audio in/out
	if call.DurationSeconds > 0 && call.EstimatedCostUSD == 0 && !call.UsedCustomKey {
		minutes := float64(call.DurationSeconds) / 60.0
		call.EstimatedCostUSD = minutes * 0.012
	}

	_, err := d.conn.Exec(`
		INSERT INTO call_logs (id, user_id, persona_id, started_at, ended_at, duration_seconds, used_custom_key, estimated_cost_usd)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, call.ID, call.UserID, call.PersonaID, call.StartedAt, call.EndedAt, call.DurationSeconds, call.UsedCustomKey, call.EstimatedCostUSD)
	return err
}

func (d *DB) CheckUserQuota(userID string) (allowed bool, usedMinutes int, limitMinutes int, isCustomKey bool, customKey string, err error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var u User
	var cKey sql.NullString
	err = d.conn.QueryRow(`
		SELECT id, role, status, daily_limit_minutes, custom_api_key 
		FROM users 
		WHERE id = ?
	`, userID).Scan(&u.ID, &u.Role, &u.Status, &u.DailyLimitMinutes, &cKey)
	if err != nil {
		return false, 0, 0, false, "", err
	}

	if cKey.Valid {
		u.CustomAPIKey = strings.TrimSpace(cKey.String)
	}

	// 1. Status check: pending or blocked cannot make calls
	if u.Status != "active" {
		return false, 0, u.DailyLimitMinutes, false, "", fmt.Errorf("account status is %s (awaiting admin approval)", u.Status)
	}

	// 2. Admin has unlimited quota
	if u.Role == "admin" {
		if u.CustomAPIKey != "" {
			return true, 0, 9999, true, u.CustomAPIKey, nil
		}
		return true, 0, 9999, false, "", nil
	}

	// 3. User with Custom Key (BYOK) has unlimited quota on their own key
	if u.CustomAPIKey != "" {
		return true, 0, u.DailyLimitMinutes, true, u.CustomAPIKey, nil
	}

	// 4. Central key quota check: calculate today's total call duration
	todayStart := time.Now().Format("2006-01-02 00:00:00")
	var totalSeconds sql.NullInt64
	err = d.conn.QueryRow(`
		SELECT SUM(duration_seconds)
		FROM call_logs
		WHERE user_id = ? AND started_at >= ? AND used_custom_key = 0
	`, userID, todayStart).Scan(&totalSeconds)
	if err != nil && err != sql.ErrNoRows {
		return false, 0, u.DailyLimitMinutes, false, "", err
	}

	sec := 0
	if totalSeconds.Valid {
		sec = int(totalSeconds.Int64)
	}
	usedMinutes = sec / 60
	limitMinutes = u.DailyLimitMinutes

	if usedMinutes >= limitMinutes {
		return false, usedMinutes, limitMinutes, false, "", nil
	}

	return true, usedMinutes, limitMinutes, false, "", nil
}

// Admin Dashboard stats
func (d *DB) GetAllUsersWithStats() ([]AdminUserView, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	todayStart := time.Now().Format("2006-01-02 00:00:00")

	rows, err := d.conn.Query(`
		SELECT 
			u.id, 
			u.email, 
			COALESCE(p.display_name, u.email) as display_name,
			u.auth_provider, 
			u.role, 
			u.status, 
			u.daily_limit_minutes,
			COALESCE(u.custom_api_key, '') as custom_api_key,
			u.created_at,
			COALESCE(stats.total_calls, 0) as total_calls,
			COALESCE(stats.total_duration_sec, 0) as total_duration_sec,
			COALESCE(stats.total_cost_usd, 0.0) as total_cost_usd,
			COALESCE(stats.last_active, '') as last_active,
			COALESCE(today_stats.today_duration_sec, 0) as today_duration_sec
		FROM users u
		LEFT JOIN user_profile p ON u.id = p.user_id
		LEFT JOIN (
			SELECT 
				user_id, 
				COUNT(id) as total_calls, 
				SUM(duration_seconds) as total_duration_sec, 
				SUM(estimated_cost_usd) as total_cost_usd, 
				MAX(ended_at) as last_active
			FROM call_logs
			GROUP BY user_id
		) stats ON u.id = stats.user_id
		LEFT JOIN (
			SELECT 
				user_id, 
				SUM(duration_seconds) as today_duration_sec
			FROM call_logs
			WHERE started_at >= ?
			GROUP BY user_id
		) today_stats ON u.id = today_stats.user_id
		ORDER BY u.created_at ASC
	`, todayStart)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := make([]AdminUserView, 0)
	for rows.Next() {
		var v AdminUserView
		var customKey string
		var todayDurationSec int

		err := rows.Scan(
			&v.ID,
			&v.Email,
			&v.DisplayName,
			&v.AuthProvider,
			&v.Role,
			&v.Status,
			&v.DailyLimitMinutes,
			&customKey,
			&v.CreatedAt,
			&v.TotalCalls,
			&v.TotalDurationSec,
			&v.TotalCostUSD,
			&v.LastActive,
			&todayDurationSec,
		)
		if err != nil {
			return nil, err
		}

		v.HasCustomKey = (customKey != "")
		v.MaskedCustomKey = maskAPIKey(customKey)
		v.TodayUsedMinutes = todayDurationSec / 60
		list = append(list, v)
	}

	return list, rows.Err()
}

// EnsureAdminEmails ensures all configured admin emails have active admin accounts
func (d *DB) EnsureAdminEmails(adminEmails []string) error {
	for _, rawEmail := range adminEmails {
		email := strings.ToLower(strings.TrimSpace(rawEmail))
		if email == "" {
			continue
		}

		user, err := d.GetUserByEmail(email)
		if err != nil {
			return err
		}

		if user == nil {
			// Pre-create admin user with clean human display name
			namePart := strings.Split(email, "@")[0]
			namePart = strings.ReplaceAll(namePart, ".", " ")
			namePart = strings.ReplaceAll(namePart, "_", " ")
			namePart = strings.ReplaceAll(namePart, "-", " ")
			namePart = strings.Title(strings.TrimSpace(namePart))
			_, err = d.CreateUser(email, "", "google", "", "admin", "active", namePart)
			if err != nil {
				return err
			}
		} else {
			// Promote existing user to active admin
			if user.Role != "admin" || user.Status != "active" {
				_ = d.UpdateUserRole(user.ID, "admin")
				_ = d.UpdateUserStatus(user.ID, "active")
			}
			// Clean up any legacy dot-separated display name in profile
			if p, pErr := d.GetUserProfile(user.ID); pErr == nil && p != nil {
				if strings.Contains(p.DisplayName, ".") || strings.Contains(p.DisplayName, "_") {
					clean := strings.ReplaceAll(p.DisplayName, ".", " ")
					clean = strings.ReplaceAll(clean, "_", " ")
					phonetic := p.PhoneticNote
					if phonetic == "" {
						phonetic = clean
					}
					_ = d.UpdateUserProfile(user.ID, clean, phonetic)
				}
			}
		}
	}
	return nil
}
