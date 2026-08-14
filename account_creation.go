package main

import (
	"claw/internal/config"
	"crypto/sha256"
	"fmt"
	"log"
	"maps"
	"strings"
	"time"

	"claw/internal/pwhash"

	"github.com/google/uuid"
)

type AccountCreateInput struct {
	Username      Username
	Password      string
	Email         string
	System        System
	Provider      string
	RequestIP     string
	RequestOrigin string
	ExtraSys      map[string]any
}

func createAccount(in AccountCreateInput) (User, error) {
	usernameLower := in.Username.ToLower()
	if usernameLower == "" {
		return nil, fmt.Errorf("username is required")
	}
	if in.Email == "" {
		return nil, fmt.Errorf("email is required")
	}
	if in.System.Name == "" {
		return nil, fmt.Errorf("system is required")
	}

	emailLower := strings.ToLower(strings.TrimSpace(in.Email))

	usersMutex.Lock()
	defer usersMutex.Unlock()

	idToUserMutex.RLock()
	_, usernameTaken := usernameToId[usernameLower]
	_, emailTaken := emailToId[emailLower]
	idToUserMutex.RUnlock()
	if usernameTaken {
		return nil, fmt.Errorf("username already in use")
	}
	if emailTaken {
		return nil, fmt.Errorf("email already in use")
	}

	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(in.RequestIP)))
	provider := in.Provider
	if provider == "" {
		provider = "unknown"
	}
	origin := in.RequestOrigin
	if origin == "" {
		origin = "unknown"
	}

	go sendDiscordWebhook([]map[string]any{
		{
			"title":       "New Account Registered",
			"description": fmt.Sprintf("**Username:** %s\n**Email:** %s\n**System:** %s\n**Provider:** %s\n**IP:** %s\n**Host:** %s", in.Username, in.Email, in.System.Name, provider, hash, origin),
			"color":       0x57cdac,
			"timestamp":   time.Now().Format(time.RFC3339),
		},
	})

	newUser := User{
		"username":         string(in.Username),
		"pfp":              avatarURL(usernameLower),
		"password":         "",
		"email":            in.Email,
		"key":              generateAccountToken(),
		"system":           in.System.Name,
		"max_size":         5000000,
		"sys.last_login":   time.Now().UnixMilli(),
		"sys.total_logins": 0,
		"sys.friends":      []string{},
		"sys.requests":     []string{},
		"sys.links":        []map[string]any{},
		"sys.currency":     float64(0),
		"sys.transactions": []any{},
		"sys.items":        []any{},
		"sys.badges":       []string{},
		"sys.purchases":    []any{},
		"private":          false,
		"sys.id":           uuid.New().String(),
		"sys.passv":        1,
		"theme": map[string]any{
			"primary":    "#222",
			"secondary":  "#555",
			"tertiary":   "#777",
			"text":       "#fff",
			"background": "#050505",
			"accent":     "#57cdac",
		},
		"onboot": []string{
			"Origin/(A) System/System Apps/originWM.osl",
			"Origin/(A) System/System Apps/Desktop.osl",
			"Origin/(A) System/Docks/Dock.osl",
			"Origin/(A) System/System Apps/Quick_Settings.osl",
		},
		"created":                time.Now().UnixMilli(),
		"wallpaper":              in.System.Wallpaper,
		"sys.tos_accepted":       false,
		"sys.email_verified":     false,
		"sys.email_verify_token": "",
	}

	if in.ExtraSys != nil {
		maps.Copy(newUser, in.ExtraSys)
	}

	salt := getOrCreateSalt(newUser)
	newUser["password"] = pwhash.HashPBKDF2(in.Password, salt, config.PBKDF2_ITERATIONS)

	newUser["sys.index"] = nextUserIndex()

	users = append(users, newUser)
	newId := newUser.GetId()
	idToUserMutex.Lock()
	usernameToId[usernameLower] = newId
	idToUser[newId] = newUser
	if k := newUser.GetKey(); k != "" {
		keyToId[k] = newId
	}
	if emailLower != "" {
		emailToId[emailLower] = newId
	}
	idToUserMutex.Unlock()
	saveUser(newId)
	return newUser, nil
}

// ensureSystemAccounts creates the built-in system accounts that the credit
// economy relies on ("rotur" as the mint for sign-in / gift / escrow credits,
// "mist" as the default tax recipient) whenever they are missing from the
// store. It is safe to call on every startup.
func ensureSystemAccounts() {
	ensureSystemAccount(Username("rotur"))
	ensureSystemAccount(Username("mist"))
}

// ensureSystemAccount creates a single built-in system account if it does not
// already exist. These accounts use an unusable (empty) password and a
// reserved .local email so they cannot be logged into or re-registered.
func ensureSystemAccount(username Username) {
	name := username.ToLower()
	if _, err := getAccountByUsername(name); err == nil {
		return
	}

	newUser := User{
		"username":              string(username),
		"email":                 string(username) + "@system.local",
		"password":              "",
		"key":                   generateAccountToken(),
		"system":                "rotur",
		"max_size":              5000000,
		"sys.last_login":        time.Now().UnixMilli(),
		"sys.total_logins":      0,
		"sys.friends":           []string{},
		"sys.requests":          []string{},
		"sys.links":             []map[string]any{},
		"sys.currency":          float64(0),
		"sys.transactions":      []any{},
		"sys.items":             []any{},
		"sys.badges":            []string{},
		"sys.purchases":         []any{},
		"sys.id":                uuid.New().String(),
		"sys.passv":             1,
		"sys.tos_accepted":      true,
		"sys.email_verified":    true,
		"created":               time.Now().UnixMilli(),
		"sys.index":             nextUserIndex(),
	}

	usersMutex.Lock()
	users = append(users, newUser)
	usersMutex.Unlock()

	newId := newUser.GetId()
	idToUserMutex.Lock()
	usernameToId[name] = newId
	idToUser[newId] = newUser
	if k := newUser.GetKey(); k != "" {
		keyToId[k] = newId
	}
	if email := newUser.GetEmail(); email != "" {
		emailToId[email] = newId
	}
	idToUserMutex.Unlock()
	saveUser(newId)

	log.Printf("[init] Created built-in system account %q", username)
}
