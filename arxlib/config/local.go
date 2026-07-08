package config

import (
	"encoding/json"
	"os"
)

const localConfigPath = "config/local.json"

// LocalConfig holds user-specific overrides saved in config/local.json.
// This file is gitignored — it is the only place the DB password is persisted on disk.
type LocalConfig struct {
	DBServer            string `json:"db_server"`
	DBName              string `json:"db_name"`
	DBUser              string `json:"db_user"`
	DBPassword          string `json:"db_password"`
	DocControlRoot      string `json:"doc_control_root"`
	POFolderRoot        string `json:"po_folder_root"`
	SupplierFilesRoot   string `json:"supplier_files_root"`
	ImageRoot           string `json:"image_root"`
	DebugMode           bool   `json:"debug_mode"`
	TestMode            *bool  `json:"test_mode,omitempty"`
	TestDBName          string `json:"test_db_name,omitempty"`
}

// LoadLocal reads config/local.json. On first run after upgrading from the two-app
// setup, it migrates config/local.pm.json + config/local.tr.json into the new file.
func LoadLocal() (*LocalConfig, error) {
	data, err := os.ReadFile(localConfigPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		if lc := migrateLegacy(); lc != nil {
			_ = SaveLocal(lc)
			return lc, nil
		}
		return &LocalConfig{}, nil
	}
	var lc LocalConfig
	if err := json.Unmarshal(data, &lc); err != nil {
		return nil, err
	}
	return &lc, nil
}

func SaveLocal(lc *LocalConfig) error {
	if err := os.MkdirAll("config", 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(lc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(localConfigPath, data, 0600)
}

// migrateLegacy reads the old per-app config files and merges them.
// PM fields win for shared values (they carry the DB password).
func migrateLegacy() *LocalConfig {
	type legacyPM struct {
		DBServer            string `json:"db_server"`
		DBName              string `json:"db_name"`
		DBUser              string `json:"db_user"`
		DBPassword          string `json:"db_password"`
		DocControlRoot      string `json:"doc_control_root"`
		POFolderRoot        string `json:"po_folder_root"`
		SupplierFilesRoot   string `json:"supplier_files_root"`
		DebugMode           bool   `json:"debug_mode"`
		TestMode            *bool  `json:"test_mode,omitempty"`
		TestDBName          string `json:"test_db_name,omitempty"`
	}
	type legacyTR struct {
		DBServer       string `json:"db_server"`
		DBName         string `json:"db_name"`
		DBUser         string `json:"db_user"`
		DBPassword     string `json:"db_password"`
		ImageRoot      string `json:"image_root"`
		DocControlRoot string `json:"doc_control_root"`
		DebugMode      bool   `json:"debug_mode"`
		TestMode       *bool  `json:"test_mode,omitempty"`
		TestDBName     string `json:"test_db_name,omitempty"`
	}

	var pm legacyPM
	var tr legacyTR
	var found bool

	if data, err := os.ReadFile("config/local.pm.json"); err == nil {
		if json.Unmarshal(data, &pm) == nil {
			found = true
		}
	}
	if data, err := os.ReadFile("config/local.tr.json"); err == nil {
		if json.Unmarshal(data, &tr) == nil {
			found = true
		}
	}
	if !found {
		return nil
	}

	lc := &LocalConfig{
		DBServer:            pm.DBServer,
		DBName:              pm.DBName,
		DBUser:              pm.DBUser,
		DBPassword:          pm.DBPassword,
		DocControlRoot:      pm.DocControlRoot,
		POFolderRoot:        pm.POFolderRoot,
		SupplierFilesRoot:   pm.SupplierFilesRoot,
		ImageRoot:           tr.ImageRoot,
		DebugMode:           pm.DebugMode,
		TestMode:            pm.TestMode,
		TestDBName:          pm.TestDBName,
	}
	if lc.DBServer == "" {
		lc.DBServer = tr.DBServer
	}
	if lc.DBName == "" {
		lc.DBName = tr.DBName
	}
	if lc.DBUser == "" {
		lc.DBUser = tr.DBUser
	}
	if lc.DBPassword == "" {
		lc.DBPassword = tr.DBPassword
	}
	if lc.DocControlRoot == "" {
		lc.DocControlRoot = tr.DocControlRoot
	}
	if lc.TestMode == nil {
		lc.TestMode = tr.TestMode
	}
	if lc.TestDBName == "" {
		lc.TestDBName = tr.TestDBName
	}
	return lc
}
