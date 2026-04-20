package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/khanakia/repolink-go/internal/app"
	"github.com/khanakia/repolink-go/internal/config"
)

// MVP-18 config verbs.
//
//	repolink config                                 show resolved active config
//	repolink config --list                          show all profiles + default
//	repolink config --get <key>
//	repolink config --set <key> <value>             scalar keys only
//	repolink config --unset <key>
//	repolink config --add-profile <name> --dir <p>
//	repolink config --add-scan-root <path>          active profile
//	repolink config --remove-scan-root <path>
//
// Every write preserves existing comments + formatting via hujson.Patch.

func newConfigCmd(a *app.App) *cobra.Command {
	var (
		listFlag       bool
		getKey         string
		setKey         string
		unsetKey       string
		addProfileName string
		addProfileDir  string
		addScanRoot    string
		removeScanRoot string
	)

	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or edit ~/.repolink/config.jsonc",
		Long: `repolink config — show or edit the machine config.

Usage patterns:
  repolink config                                  show resolved active config
  repolink config --list                           every profile + default
  repolink config --get <key>
  repolink config --set <key> <value>              scalar only
  repolink config --unset <key>
  repolink config --add-profile <n> --dir <path>
  repolink config --add-scan-root <path>           active profile
  repolink config --remove-scan-root <path>`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			setValue := ""
			if setKey != "" {
				if len(args) != 1 {
					return errors.New("--set requires a value: repolink config --set <key> <value>")
				}
				setValue = args[0]
			}
			return runConfig(cmd.Context(), a, configOpts{
				List:           listFlag,
				Get:            getKey,
				SetKey:         setKey,
				SetValue:       setValue,
				Unset:          unsetKey,
				AddProfileName: addProfileName,
				AddProfileDir:  addProfileDir,
				AddScanRoot:    addScanRoot,
				RemoveScanRoot: removeScanRoot,
			})
		},
	}
	cmd.Flags().BoolVar(&listFlag, "list", false, "print the full config (all profiles + default)")
	cmd.Flags().StringVar(&getKey, "get", "", "read a dotted key (e.g. dir, scan_roots, profiles.work.dir)")
	cmd.Flags().StringVar(&setKey, "set", "", "scalar key to set (value as positional arg)")
	cmd.Flags().StringVar(&unsetKey, "unset", "", "remove a key")
	cmd.Flags().StringVar(&addProfileName, "add-profile", "", "scaffold a new profile (requires --dir)")
	cmd.Flags().StringVar(&addProfileDir, "dir", "", "dir for --add-profile")
	cmd.Flags().StringVar(&addScanRoot, "add-scan-root", "", "append to active profile's scan_roots")
	cmd.Flags().StringVar(&removeScanRoot, "remove-scan-root", "", "remove from active profile's scan_roots")
	return cmd
}

type configOpts struct {
	List           bool
	Get            string
	SetKey         string
	SetValue       string
	Unset          string
	AddProfileName string
	AddProfileDir  string
	AddScanRoot    string
	RemoveScanRoot string
}

// Tag which mutually-exclusive mode is active so we can route.
func (o configOpts) mode() string {
	switch {
	case o.List:
		return "list"
	case o.Get != "":
		return "get"
	case o.SetKey != "":
		return "set"
	case o.Unset != "":
		return "unset"
	case o.AddProfileName != "":
		return "add_profile"
	case o.AddScanRoot != "":
		return "add_scan_root"
	case o.RemoveScanRoot != "":
		return "remove_scan_root"
	}
	return "show"
}

func runConfig(ctx context.Context, a *app.App, opts configOpts) error {
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return err
	}
	if a.ConfigPath != "" {
		cfgPath = a.ConfigPath
	}

	mode := opts.mode()

	// Special case: `config --add-profile <n> --dir <p>` bootstraps if missing.
	if mode == "add_profile" {
		return runConfigAddProfile(a, cfgPath, opts.AddProfileName, opts.AddProfileDir)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return fmt.Errorf("no config at %s — run `repolink setup` first", cfgPath)
		}
		return err
	}

	switch mode {
	case "list":
		return renderConfigList(a, cfg)
	case "show":
		return renderConfigShow(a, cfg)
	case "get":
		val, err := cfg.Get(opts.Get)
		if err != nil {
			return err
		}
		return renderConfigValue(a, opts.Get, val)
	case "set":
		if err := cfg.Set(opts.SetKey, opts.SetValue); err != nil {
			return err
		}
		if err := cfg.WriteFile(); err != nil {
			return err
		}
		return renderConfigOK(a, fmt.Sprintf("set %s = %s", opts.SetKey, opts.SetValue))
	case "unset":
		if err := cfg.Unset(opts.Unset); err != nil {
			return err
		}
		if err := cfg.WriteFile(); err != nil {
			return err
		}
		return renderConfigOK(a, "unset "+opts.Unset)
	case "add_scan_root":
		if err := cfg.AddScanRoot(opts.AddScanRoot); err != nil {
			return err
		}
		if err := cfg.WriteFile(); err != nil {
			return err
		}
		return renderConfigOK(a, "added scan_root: "+opts.AddScanRoot)
	case "remove_scan_root":
		if err := cfg.RemoveScanRoot(opts.RemoveScanRoot); err != nil {
			return err
		}
		if err := cfg.WriteFile(); err != nil {
			return err
		}
		return renderConfigOK(a, "removed scan_root: "+opts.RemoveScanRoot)
	}
	return nil
}

func runConfigAddProfile(a *app.App, cfgPath, name, dir string) error {
	if dir == "" {
		return errors.New("--add-profile requires --dir")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			cfg = config.BootstrapEmpty(cfgPath)
		} else {
			return err
		}
	}
	if err := cfg.AddProfile(name, config.Profile{Dir: dir}); err != nil {
		return err
	}
	if err := cfg.WriteFile(); err != nil {
		return err
	}
	return renderConfigOK(a, fmt.Sprintf("added profile %q → %s", name, dir))
}

type configShow struct {
	Profile        string   `json:"profile"`
	Dir            string   `json:"dir"`
	ScanRoots      []string `json:"scan_roots,omitempty"`
	DefaultProfile string   `json:"default_profile"`
}

func renderConfigShow(a *app.App, cfg *config.Config) error {
	name, prof, err := cfg.Resolve(a.ProfileOverride)
	if err != nil {
		return err
	}
	payload := configShow{
		Profile:        name,
		Dir:            prof.Dir,
		ScanRoots:      prof.ScanRoots,
		DefaultProfile: cfg.DefaultProfile,
	}
	if a.JSON {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{
			"ok": true, "version": app.Version, "data": payload,
		})
	}
	fmt.Fprintf(a.Stdout, "profile:         %s\n", payload.Profile)
	fmt.Fprintf(a.Stdout, "dir:             %s\n", payload.Dir)
	fmt.Fprintf(a.Stdout, "default_profile: %s\n", payload.DefaultProfile)
	if len(payload.ScanRoots) > 0 {
		fmt.Fprintln(a.Stdout, "scan_roots:")
		for _, r := range payload.ScanRoots {
			fmt.Fprintf(a.Stdout, "  - %s\n", r)
		}
	}
	return nil
}

func renderConfigList(a *app.App, cfg *config.Config) error {
	if a.JSON {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{
			"ok":      true,
			"version": app.Version,
			"data": map[string]any{
				"default_profile": cfg.DefaultProfile,
				"profiles":        cfg.Profiles,
			},
		})
	}
	fmt.Fprintf(a.Stdout, "default_profile: %s\n\n", cfg.DefaultProfile)
	names := make([]string, 0, len(cfg.Profiles))
	for n := range cfg.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := cfg.Profiles[n]
		marker := "  "
		if n == cfg.DefaultProfile {
			marker = "* "
		}
		fmt.Fprintf(a.Stdout, "%s%s\n", marker, n)
		fmt.Fprintf(a.Stdout, "    dir: %s\n", p.Dir)
		if len(p.ScanRoots) > 0 {
			fmt.Fprintln(a.Stdout, "    scan_roots:")
			for _, r := range p.ScanRoots {
				fmt.Fprintf(a.Stdout, "      - %s\n", r)
			}
		}
	}
	return nil
}

func renderConfigValue(a *app.App, key string, val any) error {
	if a.JSON {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{
			"ok":      true,
			"version": app.Version,
			"data":    map[string]any{"key": key, "value": val},
		})
	}
	switch x := val.(type) {
	case []string:
		for _, v := range x {
			fmt.Fprintln(a.Stdout, v)
		}
	default:
		fmt.Fprintln(a.Stdout, x)
	}
	return nil
}

func renderConfigOK(a *app.App, msg string) error {
	if a.JSON {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{
			"ok": true, "version": app.Version,
			"data": map[string]any{"message": msg},
		})
	}
	fmt.Fprintln(a.Stdout, "✓ "+msg)
	return nil
}
