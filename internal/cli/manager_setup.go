package cli

import (
	"encoding/json"
	"github.com/digitalygo/smidja/internal/envai"
	"github.com/digitalygo/smidja/internal/manager"
)

type managerSetupConfig struct {
	EnvaiURL    string `json:"envai_url"`
	EnvaiKeyEnv string `json:"envai_key_env"`
	Namespace   string `json:"namespace"`
	Model       string `json:"model"`
}

func runManagerSetup(args []string, d *Deps) error {
	var cfg managerSetupConfig
	if err := readServiceConfig(args, "manager-setup", d, &cfg); err != nil {
		return fail(d, err)
	}
	key, err := referencedSecret(d, cfg.EnvaiKeyEnv, "Envai API key", 1)
	if err != nil {
		return fail(d, err)
	}
	remote, err := envai.New(envai.Config{BaseURL: cfg.EnvaiURL, APIKey: key, HTTPClient: d.HTTPClient})
	if err != nil {
		return fail(d, err)
	}
	result, setupErr := manager.Bootstrap(d.Context, remote, cfg.Namespace, cfg.Model)
	encoder := json.NewEncoder(d.Stdout)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(result); err != nil {
		return fail(d, err)
	}
	if setupErr != nil {
		return fail(d, setupErr)
	}
	return nil
}
