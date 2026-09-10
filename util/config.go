package util

import (
	"os"
	"time"

	"github.com/spf13/viper"
)

// Config holds all application configuration. Values are read by viper from
// app.env (or environment variables, which take precedence).
type Config struct {
	BraveSearchAPIKey    string        `mapstructure:"BRAVE_SEARCH_API_KEY"`
	Environment          string        `mapstructure:"ENVIRONMENT"`
	DBSource             string        `mapstructure:"DB_SOURCE"`
	MigrationURL         string        `mapstructure:"MIGRATION_URL"`
	HTTPServerAddress    string        `mapstructure:"HTTP_SERVER_ADDRESS"`
	TokenSymmetricKey    string        `mapstructure:"TOKEN_SYMMETRIC_KEY"`
	AccessTokenDuration  time.Duration `mapstructure:"ACCESS_TOKEN_DURATION"`
	RefreshTokenDuration time.Duration `mapstructure:"REFRESH_TOKEN_DURATION"`
	AllowedOrigins       []string      `mapstructure:"ALLOWED_ORIGINS"`

	// StoreKit 2 / App Store. AppleRootCAPath points at Apple Root CA - G3
	// (PEM or DER); empty disables /subscription/verify and /webhooks/apple.
	// AppleBundleID, if set, pins the verified transaction's bundle id.
	AppleRootCAPath string `mapstructure:"APPLE_ROOT_CA_PATH"`
	AppleBundleID   string `mapstructure:"APPLE_BUNDLE_ID"`

	// ImageBucket is the GCS bucket for uploaded restaurant/meal photos.
	// Empty disables /internal/uploads/image (returns 501).
	ImageBucket string `mapstructure:"IMAGE_BUCKET"`

	// Transactional mail, used only for operator password-reset links. An empty
	// ResendAPIKey disables delivery: /internal/auth/forgot-password then
	// returns 501 and reset links are written to the log instead.
	// MailFrom must be on a domain verified in the Resend account.
	ResendAPIKey string `mapstructure:"RESEND_API_KEY"`
	MailFrom     string `mapstructure:"MAIL_FROM"`
	MailFromName string `mapstructure:"MAIL_FROM_NAME"`

	// AdminPortalURL is the public origin of the gurufuri-admin SPA. It is the
	// base for emailed reset links, so it must be set for the reset flow to
	// produce a working URL (e.g. https://gurufuri-admin.web.app).
	AdminPortalURL string `mapstructure:"ADMIN_PORTAL_URL"`

	// PasswordResetTokenDuration is how long an emailed reset link stays valid.
	// Defaults to 1h when unset.
	PasswordResetTokenDuration time.Duration `mapstructure:"PASSWORD_RESET_TOKEN_DURATION"`
}

// envKeys are bound explicitly so that, in a container with no app.env file,
// values can be supplied purely through environment variables (12-factor).
var envKeys = []string{
	"ENVIRONMENT", "DB_SOURCE", "MIGRATION_URL", "HTTP_SERVER_ADDRESS",
	"TOKEN_SYMMETRIC_KEY", "ACCESS_TOKEN_DURATION", "REFRESH_TOKEN_DURATION",
	"ALLOWED_ORIGINS", "APPLE_ROOT_CA_PATH", "APPLE_BUNDLE_ID",
	"IMAGE_BUCKET", "BRAVE_SEARCH_API_KEY",
	"RESEND_API_KEY", "MAIL_FROM", "MAIL_FROM_NAME", "ADMIN_PORTAL_URL",
	"PASSWORD_RESET_TOKEN_DURATION",
}

// LoadConfig reads configuration from app.env in the given path, with
// environment variables overriding file values. A missing app.env is not an
// error: the service then runs purely from environment variables.
func LoadConfig(path string) (config Config, err error) {
	viper.AddConfigPath(path)
	viper.SetConfigName("app")
	viper.SetConfigType("env")

	viper.AutomaticEnv()
	for _, key := range envKeys {
		_ = viper.BindEnv(key)
	}

	if err = viper.ReadInConfig(); err != nil {
		// No app.env file is fine — fall back to environment variables only.
		if _, notFound := err.(viper.ConfigFileNotFoundError); !notFound {
			return
		}
		err = nil
	}

	if err = viper.Unmarshal(&config); err != nil {
		return
	}

	// Serverless platforms (Cloud Run, etc.) inject PORT and require the
	// container to listen on it. Honor PORT when HTTP_SERVER_ADDRESS is unset,
	// defaulting to :8080.
	if config.HTTPServerAddress == "" {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		config.HTTPServerAddress = "0.0.0.0:" + port
	}

	if config.PasswordResetTokenDuration == 0 {
		config.PasswordResetTokenDuration = time.Hour
	}
	return
}
