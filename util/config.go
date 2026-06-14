package util

import (
	"time"

	"github.com/spf13/viper"
)

// Config holds all application configuration. Values are read by viper from
// app.env (or environment variables, which take precedence).
type Config struct {
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
}

// envKeys are bound explicitly so that, in a container with no app.env file,
// values can be supplied purely through environment variables (12-factor).
var envKeys = []string{
	"ENVIRONMENT", "DB_SOURCE", "MIGRATION_URL", "HTTP_SERVER_ADDRESS",
	"TOKEN_SYMMETRIC_KEY", "ACCESS_TOKEN_DURATION", "REFRESH_TOKEN_DURATION",
	"ALLOWED_ORIGINS", "APPLE_ROOT_CA_PATH", "APPLE_BUNDLE_ID",
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

	err = viper.Unmarshal(&config)
	return
}
