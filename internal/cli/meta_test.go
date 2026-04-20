package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMVP_16_MetaShowAndRename(t *testing.T) {
	root, _ := setupLinkedRepo(t, "n")

	out, err := runWithCapture(root, "meta", "--json")
	if err != nil {
		t.Fatalf("meta show: %v", err)
	}
	var env struct {
		Data struct {
			DisplayName   string `json:"display_name"`
			PrivateRepoID string `json:"private_repo_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	if env.Data.PrivateRepoID == "" {
		t.Error("private_repo_id empty")
	}
	origID := env.Data.PrivateRepoID

	if err := runWith(root, "meta", "rename", "Work Notes", "--json"); err != nil {
		t.Fatalf("meta rename: %v", err)
	}
	out2, _ := runWithCapture(root, "meta", "--json")
	var env2 struct {
		Data struct {
			DisplayName   string `json:"display_name"`
			PrivateRepoID string `json:"private_repo_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out2, &env2)
	if env2.Data.DisplayName != "Work Notes" {
		t.Errorf("rename did not persist: got %q", env2.Data.DisplayName)
	}
	if env2.Data.PrivateRepoID != origID {
		t.Errorf("private_repo_id changed on rename: %q → %q", origID, env2.Data.PrivateRepoID)
	}
}

func TestMVP_17_VerifyDetectsMissingAndWrongTarget(t *testing.T) {
	root, consumer := setupLinkedRepo(t, "m")
	target := filepath.Join(consumer, "research", "m")

	// 1) healthy state.
	out, _ := runWithCapture(root, "verify", "--json")
	var env struct {
		Data struct {
			Healthy int `json:"healthy"`
			Issues  []struct{ FSState string } `json:"issues"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &env)
	if env.Data.Healthy != 1 || len(env.Data.Issues) != 0 {
		t.Errorf("healthy case: got %+v", env.Data)
	}

	// 2) missing.
	_ = os.Remove(target)
	out2, _ := runWithCapture(root, "verify", "--json")
	var env2 struct {
		Data struct {
			Issues []struct {
				FSState string `json:"fs_state"`
			} `json:"issues"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out2, &env2)
	if len(env2.Data.Issues) != 1 || env2.Data.Issues[0].FSState != "missing" {
		t.Errorf("missing case: got %+v", env2.Data)
	}

	// 3) wrong target.
	if err := os.Symlink("/tmp/nowhere-should-not-exist", target); err != nil {
		t.Fatal(err)
	}
	out3, _ := runWithCapture(root, "verify", "--json")
	var env3 struct {
		Data struct {
			Issues []struct {
				FSState string `json:"fs_state"`
			} `json:"issues"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out3, &env3)
	if len(env3.Data.Issues) != 1 || env3.Data.Issues[0].FSState != "wrong_target" {
		t.Errorf("wrong_target case: got %+v", env3.Data)
	}
}
