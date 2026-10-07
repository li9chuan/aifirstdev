// Package viper provides an optional Viper-backed typed config loader.
package viper

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	mapstructure "github.com/go-viper/mapstructure/v2"
	spfviper "github.com/spf13/viper"
)

// Validator validates a decoded typed config before it is published to callers.
type Validator func(any) error

// Options controls typed config loading from Viper.
type Options struct {
	// ConfigFile is an explicit TOML/YAML/JSON file path.
	ConfigFile string
	// ConfigName is used with SearchPaths when ConfigFile is empty.
	ConfigName string
	// ConfigType is an optional config type override, for example "toml".
	ConfigType string
	// SearchPaths are searched when ConfigName is used.
	SearchPaths []string
	// EnvPrefix is prepended to environment variable names.
	EnvPrefix string
	// EnvKeys binds keys that may only exist in environment variables.
	EnvKeys []string
	// EnvKeyReplacer maps nested keys to environment variable names.
	EnvKeyReplacer *strings.Replacer
	// Defaults registers explicit defaults owned by the entrypoint or project.
	Defaults map[string]any
	// AllowUnusedKeys disables strict unused-key decode failures.
	AllowUnusedKeys bool
	// AllowMissingFile allows loading from defaults and environment only.
	AllowMissingFile bool
	// Validator validates the decoded config.
	Validator Validator
}

// Load decodes typed config from TOML/YAML/JSON, defaults, and environment.
func Load[T any](ctx context.Context, opts Options) (T, error) {
	var out T
	if err := LoadInto(ctx, opts, &out); err != nil {
		return out, err
	}
	return out, nil
}

// LoadInto decodes typed config into out.
func LoadInto(ctx context.Context, opts Options, out any) error {
	if err := validateOut(out); err != nil {
		return err
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	v := spfviper.New()
	applyOptions(v, opts)
	if err := bindEnvKeys(v, opts); err != nil {
		return err
	}
	if err := readConfig(v, opts); err != nil {
		return err
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	decodeOptions := []spfviper.DecoderConfigOption{
		spfviper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
		)),
	}
	if opts.AllowUnusedKeys {
		if err := v.Unmarshal(out, decodeOptions...); err != nil {
			return err
		}
	} else {
		if err := v.UnmarshalExact(out, decodeOptions...); err != nil {
			return err
		}
	}
	if opts.Validator != nil {
		if err := opts.Validator(out); err != nil {
			return fmt.Errorf("validate config: %w", err)
		}
	}
	return ctxErr(ctx)
}

func validateOut(out any) error {
	if out == nil {
		return fmt.Errorf("config output must not be nil")
	}
	value := reflect.ValueOf(out)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return fmt.Errorf("config output must be a non-nil pointer")
	}
	return nil
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func applyOptions(v *spfviper.Viper, opts Options) {
	if opts.ConfigFile != "" {
		v.SetConfigFile(opts.ConfigFile)
	} else {
		if opts.ConfigName != "" {
			v.SetConfigName(opts.ConfigName)
		}
		for _, path := range opts.SearchPaths {
			v.AddConfigPath(path)
		}
	}
	if opts.ConfigType != "" {
		v.SetConfigType(opts.ConfigType)
	}
	if opts.EnvPrefix != "" {
		v.SetEnvPrefix(opts.EnvPrefix)
	}
	replacer := opts.EnvKeyReplacer
	if replacer == nil {
		replacer = strings.NewReplacer(".", "_", "-", "_")
	}
	v.SetEnvKeyReplacer(replacer)
	v.AutomaticEnv()
	for key, value := range opts.Defaults {
		v.SetDefault(key, value)
	}
}

func bindEnvKeys(v *spfviper.Viper, opts Options) error {
	replacer := opts.EnvKeyReplacer
	if replacer == nil {
		replacer = strings.NewReplacer(".", "_", "-", "_")
	}
	for _, key := range opts.EnvKeys {
		if key == "" {
			return fmt.Errorf("env key must not be empty")
		}
		envName := strings.ToUpper(replacer.Replace(key))
		if opts.EnvPrefix != "" {
			envName = strings.ToUpper(opts.EnvPrefix) + "_" + envName
		}
		if err := v.BindEnv(key, envName); err != nil {
			return fmt.Errorf("bind env %s: %w", key, err)
		}
	}
	return nil
}

func readConfig(v *spfviper.Viper, opts Options) error {
	hasFileInput := opts.ConfigFile != "" || opts.ConfigName != ""
	if !hasFileInput {
		if opts.AllowMissingFile {
			return nil
		}
		return fmt.Errorf("config file or name is required")
	}
	if err := v.ReadInConfig(); err != nil {
		var notFound spfviper.ConfigFileNotFoundError
		if opts.AllowMissingFile && errors.As(err, &notFound) {
			return nil
		}
		return err
	}
	return nil
}
