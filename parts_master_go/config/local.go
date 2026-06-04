package config

import (
	"encoding/json"
	"os"
)

const localConfigPath = "config/local.pm.json"

// LocalConfig holds user-specific overrides stored in config/local.json.
// This file is gitignored and never committed — it is the only place the DB
// password is persisted on disk.
type LocalConfig struct {
	DBServer            string `json:"db_server"`
	DBName              string `json:"db_name"`
	DBUser              string `json:"db_user"`
	DBPassword          string `json:"db_password"`
	DocControlRoot      string `json:"doc_control_root"`
	POFolderRoot        string `json:"po_folder_root"`
	SupplierFilesRoot   string `json:"supplier_files_root"`
	PODefaultContactID  *int   `json:"po_default_contact_id,omitempty"`
	PODefaultReceiverID *int   `json:"po_default_receiver_id,omitempty"`
	DebugMode           bool   `json:"debug_mode"`
	TestMode            *bool  `json:"test_mode,omitempty"`
	TestDBName          string `json:"test_db_name,omitempty"`
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
