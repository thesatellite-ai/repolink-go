package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMVP_07_Status_ReportsFSState(t *testing.T) {
	root, consumer := setupLinkedRepo(t, "mdv")

	// Break the symlink: fs_state should become "missing".
	_ = os.Remove(filepath.Join(consumer, "research", "mdv"))

	out, err := runWithCapture(root, "status", "--json")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var env struct {
		Data struct {
			Rows []struct {
				State   string `json:"state"`
				FSState string `json:"fs_state"`
			} `json:"rows"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("parse: %v (%s)", err, out)
	}
	if len(env.Data.Rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(env.Data.Rows))
	}
	if env.Data.Rows[0].State != "active" || env.Data.Rows[0].FSState != "missing" {
		t.Errorf("want state=active fs=missing, got %+v", env.Data.Rows[0])
	}
}

func TestMVP_07_MapList_FilterByState(t *testing.T) {
	root, _ := setupLinkedRepo(t, "foo")
	// Default (no --state) lists only active.
	out, err := runWithCapture(root, "map", "list", "--json")
	if err != nil {
		t.Fatalf("map list: %v", err)
	}
	var env struct {
		Data struct {
			Rows []struct {
				State string `json:"state"`
			} `json:"rows"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	if len(env.Data.Rows) != 1 || env.Data.Rows[0].State != "active" {
		t.Errorf("default list: expected 1 active row, got %+v", env.Data.Rows)
	}

	// --state all still 1 (we have only active mappings here).
	out2, _ := runWithCapture(root, "map", "list", "--state", "all", "--json")
	var env2 struct {
		Data struct {
			Rows []struct{ State string } `json:"rows"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out2, &env2)
	if len(env2.Data.Rows) != 1 {
		t.Errorf("state=all: got %d rows", len(env2.Data.Rows))
	}

	// --state invalid → error.
	if err := runWith(root, "map", "list", "--state", "bogus", "--json"); err == nil {
		t.Error("--state bogus: expected error, got nil")
	}
}
