package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-faster/errors"
	"github.com/joho/godotenv"
)

// Config holds the credentials and paths required to run the server.
type Config struct {
	AppID            int
	AppHash          string
	Phone            string
	SessionDir       string
	HTTPAddr         string
	LogLevel         string
	FileRoot         string
	AllowSend        bool
	AllowProfileEdit bool
	AllowInlineMedia bool

	Attribution attributionMode
	Strict      bool
	AgentFooter string
	BotToken    string
	BotUsername string
}

// LoadConfig reads configuration from the environment, optionally sourcing a
// ".env" file in the working directory first.
//
// Required variables:
//
//	APP_ID, APP_HASH - obtained from https://my.telegram.org/apps
//
// Optional:
//
//	TG_PHONE         - phone number in international format (used only to name the session folder)
//	TG_SESSION_DIR   - directory to store the session (default: "./session")
//	MCP_ADDR         - address for the MCP HTTP server to listen on (default: "127.0.0.1:8080")
//	LOG_LEVEL        - log level: debug, info, warn, error (default: "info")
//	TG_FILE_ROOT     - directory from which send_file may read files (default: disabled)
//
// Agent attribution, see [attributionMode]:
//
//	TG_ATTRIBUTION        - off (default), footer, or bot
//	TG_ATTRIBUTION_STRICT - "true" fails the send when bot attribution is unavailable
//	TG_AGENT_FOOTER       - footer text (default: "sent by an agent")
//	TG_BOT_TOKEN          - echo bot token, required by "tgmcp echobot"
//	TG_BOT_USERNAME       - echo bot @username; read from echobot.json when unset
//
// Telemetry is configured by the standard OTEL_* variables read by
// [github.com/go-faster/sdk/app], see README.
//
// All rights are opt-in: set the variable to "true" to grant one.
//
//	TG_ALLOW_SEND         - enable send_message, send_file, send_reaction, send_chat_action, send_screenshot_notification
//	TG_ALLOW_PROFILE_EDIT - enable update_profile, update_profile_photo
//	TG_ALLOW_INLINE_MEDIA - allow get_file to return image bytes in the tool result instead of writing to disk
func LoadConfig() (Config, error) {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		return Config{}, errors.Wrap(err, "load .env")
	}

	var cfg Config

	appID, err := strconv.Atoi(os.Getenv("APP_ID"))
	if err != nil {
		return Config{}, errors.Wrap(err, "parse APP_ID")
	}
	cfg.AppID = appID

	cfg.AppHash = os.Getenv("APP_HASH")
	if cfg.AppHash == "" {
		return Config{}, errors.New("APP_HASH is not set")
	}

	cfg.Phone = os.Getenv("TG_PHONE")

	cfg.SessionDir = os.Getenv("TG_SESSION_DIR")
	if cfg.SessionDir == "" {
		cfg.SessionDir = "session"
	}
	cfg.SessionDir = filepath.Join(cfg.SessionDir, sessionFolder(cfg.Phone))

	cfg.HTTPAddr = os.Getenv("MCP_ADDR")
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = "127.0.0.1:8080"
	}

	cfg.LogLevel = os.Getenv("LOG_LEVEL")
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}

	cfg.FileRoot = os.Getenv("TG_FILE_ROOT")

	cfg.AllowSend = os.Getenv("TG_ALLOW_SEND") == "true"
	cfg.AllowProfileEdit = os.Getenv("TG_ALLOW_PROFILE_EDIT") == "true"
	cfg.AllowInlineMedia = os.Getenv("TG_ALLOW_INLINE_MEDIA") == "true"

	mode, err := parseAttributionMode(os.Getenv("TG_ATTRIBUTION"))
	if err != nil {
		return Config{}, err
	}
	cfg.Attribution = mode
	cfg.Strict = os.Getenv("TG_ATTRIBUTION_STRICT") == "true"

	cfg.AgentFooter = os.Getenv("TG_AGENT_FOOTER")
	if cfg.AgentFooter == "" {
		cfg.AgentFooter = defaultAgentFooter
	}

	cfg.BotToken = os.Getenv("TG_BOT_TOKEN")
	cfg.BotUsername = strings.TrimPrefix(os.Getenv("TG_BOT_USERNAME"), "@")

	if cfg.Attribution == attributionBot && cfg.BotToken == "" && cfg.BotUsername == "" {
		return Config{}, errors.New("TG_ATTRIBUTION=bot requires TG_BOT_TOKEN or TG_BOT_USERNAME")
	}

	return cfg, nil
}

// sessionFolder derives a stable subdirectory name from a phone number.
// When phone is empty, "default" is returned.
func sessionFolder(phone string) string {
	if phone == "" {
		return "default"
	}

	var out []rune
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
	}

	return "phone-" + string(out)
}
