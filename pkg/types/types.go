package types

import (
	"os"
	"time"
)

type FileInfo struct {
	Path        string
	Dir         string
	Name        string
	Ext         string
	Size        int64
	ModTime     time.Time
	CreateTime  time.Time
	Mode        os.FileMode
	IsDir       bool
	Hash        string
	ExifDate    *time.Time
	MatchedGroups map[string]string
}

type RenameAction struct {
	ID          string
	SourcePath  string
	TargetPath  string
	SourceFile  FileInfo
	TargetFile  FileInfo
	Operation   OperationType
	RuleName    string
	Executed    bool
	Timestamp   time.Time
	Success     bool
	Error       string
	Steps       []RuleStep
}

type RuleStep struct {
	Rule     string
	Before   string
	After    string
	Applied  bool
}

type OperationType string

const (
	OpRename   OperationType = "rename"
	OpMove     OperationType = "move"
	OpCopy     OperationType = "copy"
	OpDelete   OperationType = "delete"
	OpDedup    OperationType = "dedup"
)

type Rule interface {
	Name() string
	Apply(f *FileInfo, index int) (string, bool, error)
}

type RuleConfig struct {
	Sequence  *SequenceConfig  `toml:"sequence"`
	DateTime  *DateTimeConfig  `toml:"datetime"`
	Case      *CaseConfig      `toml:"case"`
	Regex     *RegexConfig     `toml:"regex"`
	Dedup     *DedupConfig     `toml:"dedup"`
	Classify  *ClassifyConfig  `toml:"classify"`
	Plugins   []PluginConfig   `toml:"plugins"`
}

type SequenceConfig struct {
	Enabled   bool   `toml:"enabled"`
	Start     int    `toml:"start"`
	Step      int    `toml:"step"`
	Width     int    `toml:"width"`
	PadChar   string `toml:"pad_char"`
	Position  string `toml:"position"`
	Separator string `toml:"separator"`
	Format    string `toml:"format"`
}

type DateTimeConfig struct {
	Enabled   bool   `toml:"enabled"`
	Source    string `toml:"source"`
	Format    string `toml:"format"`
	Position  string `toml:"position"`
	Separator string `toml:"separator"`
	Fallback  string `toml:"fallback"`
}

type CaseConfig struct {
	Enabled  bool   `toml:"enabled"`
	Target   string `toml:"target"`
	AutoDetect bool `toml:"auto_detect"`
}

type RegexConfig struct {
	Enabled   bool              `toml:"enabled"`
	Pattern   string            `toml:"pattern"`
	Replace   string            `toml:"replace"`
	Groups    map[string]string `toml:"groups"`
}

type DedupConfig struct {
	Enabled     bool   `toml:"enabled"`
	Strategy    string `toml:"strategy"`
	ConflictDir string `toml:"conflict_dir"`
	Suffix      string `toml:"suffix"`
}

type ClassifyConfig struct {
	Enabled     bool              `toml:"enabled"`
	BaseDir     string            `toml:"base_dir"`
	Presets     []string          `toml:"presets"`
	Custom      map[string]string `toml:"custom"`
	CreateDirs  bool              `toml:"create_dirs"`
}

type PluginConfig struct {
	Path   string            `toml:"path"`
	Args   map[string]string `toml:"args"`
}

type HashDedupConfig struct {
	Enabled       bool     `toml:"enabled"`
	Algorithm     string   `toml:"algorithm"`
	MinSize       int64    `toml:"min_size"`
	MaxSize       int64    `toml:"max_size"`
	Extensions    []string `toml:"extensions"`
	KeepStrategy  string   `toml:"keep_strategy"`
	DeleteAction  string   `toml:"delete_action"`
}

type HistoryEntry struct {
	ID        string         `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Actions   []RenameAction `json:"actions"`
	Summary   string         `json:"summary"`
	Checksum  string         `json:"checksum"`
}

type ResumeState struct {
	TaskID      string    `json:"task_id"`
	Progress    int       `json:"progress"`
	Total       int       `json:"total"`
	Processed   []string  `json:"processed"`
	Failed      []string  `json:"failed"`
	LastUpdate  time.Time `json:"last_update"`
	Checkpoint  string    `json:"checkpoint"`
}
