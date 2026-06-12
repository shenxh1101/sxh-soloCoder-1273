package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/filerename/filerename/pkg/types"
)

type Config struct {
	Global      GlobalConfig                `toml:"global"`
	Rename      types.RuleConfig            `toml:"rename"`
	HashDedup   types.HashDedupConfig       `toml:"hash_dedup"`
	Presets     map[string]types.RuleConfig `toml:"presets"`
	PluginPaths []string                    `toml:"plugin_paths"`
}

type GlobalConfig struct {
	Workers      int    `toml:"workers"`
	Preview      bool   `toml:"preview"`
	Recursive    bool   `toml:"recursive"`
	Verbose      bool   `toml:"verbose"`
	Backup       bool   `toml:"backup"`
	BackupDir    string `toml:"backup_dir"`
	HistoryFile  string `toml:"history_file"`
	HistoryLimit int    `toml:"history_limit"`
	DryRun       bool   `toml:"dry_run"`
	Resume       bool   `toml:"resume"`
	ResumeFile   string `toml:"resume_file"`
	LogFile      string `toml:"log_file"`
}

func DefaultConfig() *Config {
	return &Config{
		Global: GlobalConfig{
			Workers:      32,
			Preview:      false,
			Recursive:    true,
			Verbose:      false,
			Backup:       false,
			BackupDir:    "./.filerename_backup",
			HistoryFile:  "~/.filerename/history.json",
			HistoryLimit: 100,
			DryRun:       false,
			Resume:       false,
			ResumeFile:   "./.filerename_resume.json",
		},
		Rename: types.RuleConfig{
			Sequence: &types.SequenceConfig{
				Enabled:   false,
				Start:     1,
				Step:      1,
				Width:     3,
				PadChar:   "0",
				Position:  "suffix",
				Separator: "_",
			},
			DateTime: &types.DateTimeConfig{
				Enabled:   false,
				Source:    "auto",
				Format:    "2006-01-02_150405",
				Position:  "prefix",
				Separator: "_",
			},
			Case: &types.CaseConfig{
				Enabled:    false,
				Target:     "snake_case",
				AutoDetect: true,
			},
			Regex: &types.RegexConfig{
				Enabled: false,
			},
			Dedup: &types.DedupConfig{
				Enabled:     true,
				Strategy:    "suffix",
				ConflictDir: "_conflicts",
				Suffix:      "copy",
			},
			Classify: &types.ClassifyConfig{
				Enabled:    false,
				BaseDir:    "",
				Presets:    []string{"images", "videos", "documents", "code", "archives", "audio"},
				CreateDirs: true,
			},
		},
		HashDedup: types.HashDedupConfig{
			Enabled:      false,
			Algorithm:    "sha256",
			MinSize:      0,
			MaxSize:      0,
			KeepStrategy: "oldest",
			DeleteAction: "trash",
		},
		Presets:     make(map[string]types.RuleConfig),
		PluginPaths: []string{},
	}
}

func Load(path string) (*Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		return cfg, nil
	}
	absPath, err := expandPath(path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", absPath, err)
	}
	return cfg, nil
}

func LoadMerged(globalPath, projectPath string) (*Config, error) {
	gcfg, err := Load(globalPath)
	if err != nil {
		return nil, err
	}
	if projectPath == "" {
		return gcfg, nil
	}

	pcfg, err := Load(projectPath)
	if err != nil {
		return nil, err
	}

	mergeConfigs(gcfg, pcfg)
	return gcfg, nil
}

func mergeConfigs(base, override *Config) {
	if override.Global.Workers != 0 { base.Global.Workers = override.Global.Workers }
	if override.Global.Preview { base.Global.Preview = true }
	if !override.Global.Recursive { base.Global.Recursive = false }
	if override.Global.Verbose { base.Global.Verbose = true }
	if override.Global.Backup { base.Global.Backup = true }
	if override.Global.BackupDir != "" { base.Global.BackupDir = override.Global.BackupDir }
	if override.Global.HistoryFile != "" { base.Global.HistoryFile = override.Global.HistoryFile }
	if override.Global.HistoryLimit != 0 { base.Global.HistoryLimit = override.Global.HistoryLimit }
	if override.Global.DryRun { base.Global.DryRun = true }
	if override.Global.Resume { base.Global.Resume = true }
	if override.Global.ResumeFile != "" { base.Global.ResumeFile = override.Global.ResumeFile }
	if override.Global.LogFile != "" { base.Global.LogFile = override.Global.LogFile }

	if override.Rename.Sequence != nil {
		if override.Rename.Sequence.Enabled { base.Rename.Sequence = override.Rename.Sequence }
	}
	if override.Rename.DateTime != nil {
		if override.Rename.DateTime.Enabled { base.Rename.DateTime = override.Rename.DateTime }
	}
	if override.Rename.Case != nil {
		if override.Rename.Case.Enabled { base.Rename.Case = override.Rename.Case }
	}
	if override.Rename.Regex != nil {
		if override.Rename.Regex.Enabled { base.Rename.Regex = override.Rename.Regex }
	}
	if override.Rename.Dedup != nil {
		base.Rename.Dedup = override.Rename.Dedup
	}
	if override.Rename.Classify != nil {
		if override.Rename.Classify.Enabled { base.Rename.Classify = override.Rename.Classify }
	}
	if len(override.Rename.Plugins) > 0 {
		base.Rename.Plugins = append(base.Rename.Plugins, override.Rename.Plugins...)
	}

	if override.HashDedup.Enabled { base.HashDedup = override.HashDedup }

	if len(override.Presets) > 0 {
		if base.Presets == nil { base.Presets = make(map[string]types.RuleConfig) }
		for k, v := range override.Presets {
			base.Presets[k] = v
		}
	}
	if len(override.PluginPaths) > 0 {
		base.PluginPaths = append(base.PluginPaths, override.PluginPaths...)
	}
}

func FindGlobalConfig() string {
	if v := os.Getenv("FILERENAME_CONFIG"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v
		}
	}
	home, err := os.UserHomeDir()
	if err == nil {
		p := filepath.Join(home, ".config", "filerename", "config.toml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		p2 := filepath.Join(home, ".filerename.toml")
		if _, err := os.Stat(p2); err == nil {
			return p2
		}
	}
	return ""
}

func FindProjectConfig(dir string) string {
	for {
		p := filepath.Join(dir, ".filerename.toml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		p = filepath.Join(dir, "filerename.toml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func expandPath(p string) (string, error) {
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return filepath.Abs(p)
}

func SaveExample(path string) error {
	example := `# Filerename 配置文件示例

[global]
workers = 32
preview = false
recursive = true
verbose = false
backup = false
backup_dir = "./.filerename_backup"
history_file = "~/.filerename/history.json"
history_limit = 100
dry_run = false
resume = true
resume_file = "./.filerename_resume.json"
log_file = "~/.filerename/filerename.log"

[rename.sequence]
enabled = false
start = 1
step = 1
width = 3
pad_char = "0"
position = "suffix"
separator = "_"

[rename.datetime]
enabled = false
source = "auto"
format = "2006-01-02_150405"
position = "prefix"
separator = "_"

[rename.case]
enabled = false
target = "snake_case"
auto_detect = true

[rename.regex]
enabled = false
pattern = "IMG_(\\d{8})_(\\d{6})"
replace = "${1}-${2}"

[rename.dedup]
enabled = true
strategy = "suffix"
conflict_dir = "_conflicts"
suffix = "copy"

[rename.classify]
enabled = false
base_dir = ""
presets = ["images", "videos", "documents", "code", "archives", "audio"]
create_dirs = true

[rename.classify.custom]
"3d_models" = "obj,fbx,stl,blend"

[hash_dedup]
enabled = false
algorithm = "sha256"
min_size = 1024
max_size = 0
keep_strategy = "oldest"
delete_action = "trash"

[presets.photos]
[presets.photos.datetime]
enabled = true
source = "exif"
format = "2006-01-02_150405"
[presets.photos.sequence]
enabled = true
width = 4

plugin_paths = ["./plugins"]
`
	absPath, err := expandPath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(absPath, []byte(example), 0644)
}
