package config

import (
	"os"
	"testing"
)

func writeLegacyFile(t *testing.T, name, contents string) {
	t.Helper()
	if err := os.MkdirAll("config", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("config/"+name, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

func assertBoolPtr(t *testing.T, label string, got *bool, want bool) {
	t.Helper()
	if got == nil {
		t.Errorf("%s = nil, want non-nil pointer to %v", label, want)
		return
	}
	if *got != want {
		t.Errorf("%s = %v, want %v", label, *got, want)
	}
}

func TestMigrateLegacy_NoFiles(t *testing.T) {
	isolateStores(t)
	if lc := migrateLegacy(); lc != nil {
		t.Errorf("migrateLegacy() = %+v, want nil when neither legacy file exists", lc)
	}
}

func TestMigrateLegacy_PMOnly(t *testing.T) {
	isolateStores(t)
	writeLegacyFile(t, "local.pm.json", `{
		"db_server":"pmserver","db_name":"pmdb","db_user":"pmuser",
		"doc_control_root":"pmdoc","po_folder_root":"pmpo","supplier_files_root":"pmsup",
		"debug_mode":true,"test_mode":true,"test_db_name":"pmtestdb"
	}`)

	lc := migrateLegacy()
	if lc == nil {
		t.Fatal("migrateLegacy() = nil, want non-nil (PM file present)")
	}
	if lc.DBServer != "pmserver" || lc.DBName != "pmdb" || lc.DBUser != "pmuser" {
		t.Errorf("DB fields = %q/%q/%q, want pmserver/pmdb/pmuser", lc.DBServer, lc.DBName, lc.DBUser)
	}
	if lc.DocControlRoot != "pmdoc" || lc.POFolderRoot != "pmpo" || lc.SupplierFilesRoot != "pmsup" {
		t.Errorf("root fields = %q/%q/%q, want pmdoc/pmpo/pmsup", lc.DocControlRoot, lc.POFolderRoot, lc.SupplierFilesRoot)
	}
	if !lc.DebugMode {
		t.Error("DebugMode = false, want true")
	}
	assertBoolPtr(t, "TestMode", lc.TestMode, true)
	if lc.TestDBName != "pmtestdb" {
		t.Errorf("TestDBName = %q, want pmtestdb", lc.TestDBName)
	}
	if lc.ImageRoot != "" {
		t.Errorf("ImageRoot = %q, want empty (TR-only field, no TR file)", lc.ImageRoot)
	}
}

func TestMigrateLegacy_TROnly(t *testing.T) {
	isolateStores(t)
	writeLegacyFile(t, "local.tr.json", `{
		"db_server":"trserver","db_name":"trdb","db_user":"truser",
		"doc_control_root":"trdoc","image_root":"trimg",
		"debug_mode":true,"test_mode":true,"test_db_name":"trtestdb"
	}`)

	lc := migrateLegacy()
	if lc == nil {
		t.Fatal("migrateLegacy() = nil, want non-nil (TR file present)")
	}
	if lc.DBServer != "trserver" || lc.DBName != "trdb" || lc.DBUser != "truser" {
		t.Errorf("DB fields = %q/%q/%q, want trserver/trdb/truser (fallback from TR)", lc.DBServer, lc.DBName, lc.DBUser)
	}
	if lc.DocControlRoot != "trdoc" {
		t.Errorf("DocControlRoot = %q, want trdoc (fallback from TR)", lc.DocControlRoot)
	}
	assertBoolPtr(t, "TestMode", lc.TestMode, true)
	if lc.TestDBName != "trtestdb" {
		t.Errorf("TestDBName = %q, want trtestdb (fallback from TR)", lc.TestDBName)
	}
	if lc.ImageRoot != "trimg" {
		t.Errorf("ImageRoot = %q, want trimg", lc.ImageRoot)
	}
	if lc.SupplierFilesRoot != "" {
		t.Errorf("SupplierFilesRoot = %q, want empty (PM-only field, no PM file)", lc.SupplierFilesRoot)
	}
	if lc.DebugMode {
		t.Error("DebugMode = true, want false (only ever assigned from pm.DebugMode, no PM file)")
	}
}

func TestMigrateLegacy_BothFiles_PMWins(t *testing.T) {
	isolateStores(t)
	writeLegacyFile(t, "local.pm.json", `{
		"db_server":"pmserver","db_name":"pmdb","db_user":"pmuser",
		"doc_control_root":"pmdoc","test_mode":true,"test_db_name":"pmtestdb"
	}`)
	writeLegacyFile(t, "local.tr.json", `{
		"db_server":"trserver","db_name":"trdb","db_user":"truser",
		"doc_control_root":"trdoc","image_root":"trimg","test_mode":false,"test_db_name":"trtestdb"
	}`)

	lc := migrateLegacy()
	if lc == nil {
		t.Fatal("migrateLegacy() = nil, want non-nil")
	}
	if lc.DBServer != "pmserver" || lc.DBName != "pmdb" || lc.DBUser != "pmuser" {
		t.Errorf("DB fields = %q/%q/%q, want PM's values (pmserver/pmdb/pmuser)", lc.DBServer, lc.DBName, lc.DBUser)
	}
	if lc.DocControlRoot != "pmdoc" {
		t.Errorf("DocControlRoot = %q, want pmdoc (PM wins)", lc.DocControlRoot)
	}
	assertBoolPtr(t, "TestMode", lc.TestMode, true)
	if lc.TestDBName != "pmtestdb" {
		t.Errorf("TestDBName = %q, want pmtestdb (PM wins)", lc.TestDBName)
	}
	if lc.ImageRoot != "trimg" {
		t.Errorf("ImageRoot = %q, want trimg (always from TR)", lc.ImageRoot)
	}
}

func TestMigrateLegacy_PerFieldFallback(t *testing.T) {
	isolateStores(t)
	writeLegacyFile(t, "local.pm.json", `{"db_server":"pmserver"}`)
	writeLegacyFile(t, "local.tr.json", `{
		"db_server":"trserver","db_name":"trdb","db_user":"truser",
		"doc_control_root":"trdoc","test_mode":true,"test_db_name":"trtestdb"
	}`)

	lc := migrateLegacy()
	if lc == nil {
		t.Fatal("migrateLegacy() = nil, want non-nil")
	}
	if lc.DBServer != "pmserver" {
		t.Errorf("DBServer = %q, want pmserver (PM was non-empty, no fallback)", lc.DBServer)
	}
	if lc.DBName != "trdb" {
		t.Errorf("DBName = %q, want trdb (PM empty, fallback fired)", lc.DBName)
	}
	if lc.DBUser != "truser" {
		t.Errorf("DBUser = %q, want truser (PM empty, fallback fired)", lc.DBUser)
	}
	if lc.DocControlRoot != "trdoc" {
		t.Errorf("DocControlRoot = %q, want trdoc (PM empty, fallback fired)", lc.DocControlRoot)
	}
	assertBoolPtr(t, "TestMode", lc.TestMode, true)
	if lc.TestDBName != "trtestdb" {
		t.Errorf("TestDBName = %q, want trtestdb (PM empty, fallback fired)", lc.TestDBName)
	}
}

// TestMigrateLegacy_MalformedJSON documents the current silent-swallow
// behavior (issue #819) — a malformed legacy file's json.Unmarshal error
// leaves that side's fields at Go zero values without failing the merge.
// This is not asserting the behavior is correct; changing it is out of scope.
func TestMigrateLegacy_MalformedJSON(t *testing.T) {
	t.Run("PM malformed, TR valid", func(t *testing.T) {
		isolateStores(t)
		writeLegacyFile(t, "local.pm.json", `{not valid json`)
		writeLegacyFile(t, "local.tr.json", `{
			"db_server":"trserver","db_name":"trdb","db_user":"truser",
			"doc_control_root":"trdoc","test_mode":true,"test_db_name":"trtestdb"
		}`)

		lc := migrateLegacy()
		if lc == nil {
			t.Fatal("migrateLegacy() = nil, want non-nil (TR parsed successfully)")
		}
		if lc.DBServer != "trserver" || lc.DBName != "trdb" || lc.DBUser != "truser" {
			t.Errorf("DB fields = %q/%q/%q, want TR's values (PM unmarshal failed, fields stayed zero, fallback fired)", lc.DBServer, lc.DBName, lc.DBUser)
		}
		if lc.SupplierFilesRoot != "" || lc.DebugMode {
			t.Errorf("PM-only fields not zero: SupplierFilesRoot=%q DebugMode=%v", lc.SupplierFilesRoot, lc.DebugMode)
		}
	})

	t.Run("both malformed", func(t *testing.T) {
		isolateStores(t)
		writeLegacyFile(t, "local.pm.json", `{not valid json`)
		writeLegacyFile(t, "local.tr.json", `{also not valid`)

		if lc := migrateLegacy(); lc != nil {
			t.Errorf("migrateLegacy() = %+v, want nil (both files failed to parse)", lc)
		}
	})
}

func TestMigrateLegacy_TestModePointerSemantics(t *testing.T) {
	t.Run("PM omitted, TR false", func(t *testing.T) {
		isolateStores(t)
		writeLegacyFile(t, "local.pm.json", `{"db_server":"pmserver"}`)
		writeLegacyFile(t, "local.tr.json", `{"test_mode":false}`)

		lc := migrateLegacy()
		if lc == nil {
			t.Fatal("migrateLegacy() = nil, want non-nil")
		}
		assertBoolPtr(t, "TestMode", lc.TestMode, false)
	})

	t.Run("PM explicit false, TR true", func(t *testing.T) {
		isolateStores(t)
		writeLegacyFile(t, "local.pm.json", `{"test_mode":false}`)
		writeLegacyFile(t, "local.tr.json", `{"test_mode":true}`)

		lc := migrateLegacy()
		if lc == nil {
			t.Fatal("migrateLegacy() = nil, want non-nil")
		}
		assertBoolPtr(t, "TestMode", lc.TestMode, false)
	})
}
