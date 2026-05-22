package config

import (
	"encoding/json"
	"os"
)

const localConfigPath = "config/local.tr.json"

// LocalConfig holds user-specific overrides stored in config/local.json.
// This file is gitignored — it is the only place the DB password is persisted.
type LocalConfig struct {
	DBServer       string `json:"db_server"`
	DBName         string `json:"db_name"`
	DBUser         string `json:"db_user"`
	DBPassword     string `json:"db_password"`
	ImageRoot      string `json:"image_root"`
	DocControlRoot string `json:"doc_control_root"`
	DebugMode      bool   `json:"debug_mode"`
	TestMode       *bool  `json:"test_mode,omitempty"`
}

func LoadLocal() (*LocalConfig, error) {
	data, err := os.ReadFile(localConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &LocalConfig{}, nil
		}
		return nil, err
	}
	var lc LocalConfig
	if err := json.Unmarshal(data, &lc); err != nil {
		return nil, err
	}
	return &lc, nil
}

func SaveLocal(lc *LocalConfig) error {
	data, err := json.MarshalIndent(lc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(localConfigPath, data, 0600)
}
