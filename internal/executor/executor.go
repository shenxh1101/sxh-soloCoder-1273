package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/filerename/filerename/internal/config"
	"github.com/filerename/filerename/internal/exifutil"
	"github.com/filerename/filerename/internal/history"
	"github.com/filerename/filerename/internal/plugin"
	"github.com/filerename/filerename/internal/rules"
	"github.com/filerename/filerename/pkg/types"
)

type Executor struct {
	cfg       *config.Config
	rules     []types.Rule
	dedup     *rules.DedupManager
	classifier *rules.Classifier
	plugins   *plugin.PluginManager
	progress  ProgressReporter
}

type ProgressReporter interface {
	SetTotal(total int)
	Increment(delta int)
	SetPhase(phase string)
	Finish()
	Reset()
	ETA() time.Duration
}

type DefaultProgress struct {
	total    int64
	current  int64
	start    time.Time
	phase    string
	phaseMu  sync.RWMutex
}

func NewDefaultProgress() *DefaultProgress {
	return &DefaultProgress{start: time.Now()}
}

func (p *DefaultProgress) SetTotal(total int) {
	atomic.StoreInt64(&p.total, int64(total))
}
func (p *DefaultProgress) Increment(delta int) {
	atomic.AddInt64(&p.current, int64(delta))
}
func (p *DefaultProgress) SetPhase(phase string) {
	p.phaseMu.Lock()
	defer p.phaseMu.Unlock()
	p.phase = phase
}
func (p *DefaultProgress) Finish() {}
func (p *DefaultProgress) Reset() {
	atomic.StoreInt64(&p.current, 0)
}
func (p *DefaultProgress) ETA() time.Duration {
	cur := atomic.LoadInt64(&p.current)
	tot := atomic.LoadInt64(&p.total)
	if cur == 0 || tot == 0 { return 0 }
	elapsed := time.Since(p.start)
	rate := float64(cur) / elapsed.Seconds()
	if rate <= 0 { return 0 }
	remaining := float64(tot-cur) / rate
	return time.Duration(remaining) * time.Second
}
func (p *DefaultProgress) Percent() float64 {
	tot := atomic.LoadInt64(&p.total)
	if tot == 0 { return 0 }
	return float64(atomic.LoadInt64(&p.current)) / float64(tot) * 100
}
func (p *DefaultProgress) Phase() string {
	p.phaseMu.RLock()
	defer p.phaseMu.RUnlock()
	return p.phase
}

type ExecutorResult struct {
	Actions      []*types.RenameAction
	Total        int
	Succeeded    int
	Failed       int
	Skipped      int
	Elapsed      time.Duration
	HistoryID    string
	DuplicateGroups map[string][]*types.FileInfo
	Warnings     []string
}

func NewExecutor(cfg *config.Config) *Executor {
	return &Executor{cfg: cfg}
}

func (e *Executor) WithProgress(p ProgressReporter) *Executor {
	e.progress = p
	return e
}

func (e *Executor) Initialize() error {
	if e.cfg.Rename.Sequence != nil && e.cfg.Rename.Sequence.Enabled {
		e.rules = append(e.rules, rules.NewSequenceRule(e.cfg.Rename.Sequence))
	}
	if e.cfg.Rename.DateTime != nil && e.cfg.Rename.DateTime.Enabled {
		e.rules = append(e.rules, rules.NewDateTimeRule(e.cfg.Rename.DateTime))
	}
	if e.cfg.Rename.Case != nil && e.cfg.Rename.Case.Enabled {
		e.rules = append(e.rules, rules.NewCaseRule(e.cfg.Rename.Case))
	}
	if e.cfg.Rename.Regex != nil && e.cfg.Rename.Regex.Enabled {
		re, err := rules.NewRegexRule(e.cfg.Rename.Regex)
		if err != nil {
			return fmt.Errorf("invalid regex: %w", err)
		}
		e.rules = append(e.rules, re)
	}
	if e.cfg.Rename.Dedup != nil {
		e.dedup = rules.NewDedupManager(e.cfg.Rename.Dedup)
	}
	if e.cfg.Rename.Classify != nil && e.cfg.Rename.Classify.Enabled {
		e.classifier = rules.NewClassifier(e.cfg.Rename.Classify)
	}
	pluginPaths := e.cfg.PluginPaths
	e.plugins = plugin.NewPluginManager(pluginPaths)
	if len(e.cfg.Rename.Plugins) > 0 {
		if err := e.plugins.LoadPlugins(e.cfg.Rename.Plugins); err != nil {
			return fmt.Errorf("load plugins: %w", err)
		}
	}
	return nil
}

func (e *Executor) Plan(files []*types.FileInfo) ([]*types.RenameAction, error) {
	actions := make([]*types.RenameAction, 0, len(files))
	workers := e.cfg.Global.Workers
	if workers <= 0 { workers = 16 }

	if e.progress != nil {
		e.progress.SetPhase("EXIF/Metadata")
		e.progress.SetTotal(len(files))
	}

	if e.cfg.Rename.DateTime != nil && (e.cfg.Rename.DateTime.Enabled ||
		(e.cfg.Rename.DateTime.Source == "exif")) {
		exifutil.BatchExtractExif(files, workers)
	}

	if e.progress != nil {
		e.progress.SetPhase("Planning")
		e.progress.Reset()
	}

	type planResult struct {
		idx    int
		action *types.RenameAction
		skip   bool
	}

	results := make([]planResult, len(files))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for idx, f := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, file *types.FileInfo) {
			defer wg.Done()
			defer func() { <-sem }()

			action, skip := e.planSingle(file, i)
			results[i] = planResult{idx: i, action: action, skip: skip}
			if e.progress != nil {
				e.progress.Increment(1)
			}
		}(idx, f)
	}
	wg.Wait()

	for _, r := range results {
		if !r.skip && r.action != nil {
			actions = append(actions, r.action)
		}
	}
	return actions, nil
}

func (e *Executor) planSingle(f *types.FileInfo, index int) (*types.RenameAction, bool) {
	newName := f.Name
	appliedAny := false

	for _, rule := range e.rules {
		result, applied, err := rule.Apply(f, index)
		if err != nil {
			continue
		}
		if applied {
			newName = result
			appliedAny = true
		}
	}

	if e.plugins != nil {
		result, applied, err := e.plugins.ApplyAll(f, index, newName)
		if err == nil && applied {
			newName = result
			appliedAny = true
		}
	}

	targetDir := f.Dir
	if e.classifier != nil {
		if dir, ok := e.classifier.Classify(f); ok {
			targetDir = dir
			appliedAny = true
		}
	}

	if !appliedAny {
		return nil, true
	}

	if newName == "" {
		newName = f.Name
	}

	targetPath := filepath.Join(targetDir, newName+f.Ext)
	if e.dedup != nil {
		targetPath = e.dedup.ResolveConflict(targetPath)
		if targetPath == "" {
			return nil, true
		}
	}

	if targetPath == f.Path {
		return nil, true
	}

	action := &types.RenameAction{
		SourcePath: f.Path,
		TargetPath: targetPath,
		SourceFile: *f,
		Operation:  types.OpRename,
		Timestamp:  time.Now(),
	}
	if targetDir != f.Dir {
		action.Operation = types.OpMove
	}
	return action, false
}

func (e *Executor) Execute(ctx context.Context, actions []*types.RenameAction) (*ExecutorResult, error) {
	result := &ExecutorResult{
		Actions: actions,
		Total:   len(actions),
		Elapsed: time.Now(),
	}

	if len(actions) == 0 {
		return result, nil
	}

	if e.classifier != nil && e.cfg.Rename.Classify.CreateDirs {
		dirSet := make(map[string]bool)
		for _, a := range actions {
			dir := filepath.Dir(a.TargetPath)
			if dir != a.SourceFile.Dir {
				dirSet[dir] = true
			}
		}
		for dir := range dirSet {
			if err := os.MkdirAll(dir, 0755); err != nil {
				result.Warnings = append(result.Warnings, fmt.Sprintf("create dir %s: %v", dir, err))
			}
		}
	}

	workers := e.cfg.Global.Workers
	if workers <= 0 { workers = 32 }
	dryRun := e.cfg.Global.DryRun || e.cfg.Global.Preview

	if e.progress != nil {
		e.progress.SetPhase("Executing")
		e.progress.Reset()
		e.progress.SetTotal(len(actions))
	}

	var (
		wg     sync.WaitGroup
		sem    = make(chan struct{}, workers)
		mu     sync.Mutex
		succ   int32
		fail   int32
		skip   int32
	)

	for _, action := range actions {
		select {
		case <-ctx.Done():
			break
		default:
		}
		a := action
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			if dryRun {
				a.Executed = false
				a.Success = true
				mu.Lock()
				atomic.AddInt32(&succ, 1)
				mu.Unlock()
				if e.progress != nil { e.progress.Increment(1) }
				return
			}

			a.Timestamp = time.Now()
			targetDir := filepath.Dir(a.TargetPath)
			if err := os.MkdirAll(targetDir, 0755); err != nil {
				mu.Lock()
				a.Success = false
				a.Error = err.Error()
				atomic.AddInt32(&fail, 1)
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", a.SourcePath, err))
				mu.Unlock()
				if e.progress != nil { e.progress.Increment(1) }
				return
			}

			if _, err := os.Stat(a.TargetPath); err == nil {
				mu.Lock()
				a.Success = false
				a.Error = "target already exists"
				atomic.AddInt32(&skip, 1)
				mu.Unlock()
				if e.progress != nil { e.progress.Increment(1) }
				return
			}

			if err := os.Rename(a.SourcePath, a.TargetPath); err != nil {
				mu.Lock()
				a.Success = false
				a.Error = err.Error()
				atomic.AddInt32(&fail, 1)
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s -> %s: %v", a.SourcePath, a.TargetPath, err))
				mu.Unlock()
			} else {
				a.Executed = true
				a.Success = true
				atomic.AddInt32(&succ, 1)
			}
			if e.progress != nil { e.progress.Increment(1) }
		}()
	}
	wg.Wait()

	result.Succeeded = int(atomic.LoadInt32(&succ))
	result.Failed = int(atomic.LoadInt32(&fail))
	result.Skipped = int(atomic.LoadInt32(&skip))
	result.Elapsed = time.Since(result.Elapsed)
	if e.progress != nil { e.progress.Finish() }

	if !dryRun && result.Succeeded > 0 {
		hm, err := history.NewHistoryManager(e.cfg.Global.HistoryFile, e.cfg.Global.HistoryLimit)
		if err == nil {
			id, err := hm.Record(actions)
			if err == nil {
				result.HistoryID = id
			}
		}
	}
	return result, nil
}

func (e *Executor) FindDuplicateFiles(ctx context.Context, files []*types.FileInfo) (map[string][]*types.FileInfo, error) {
	if e.progress != nil {
		e.progress.SetPhase("Hashing for duplicates")
		e.progress.SetTotal(len(files))
	}

	hd := rules.NewHashDeduper(&e.cfg.HashDedup)
	dupes := hd.FindDuplicates(files, e.cfg.Global.Workers)
	if e.progress != nil {
		e.progress.Increment(len(files))
		e.progress.Finish()
	}
	return dupes, nil
}

type ResumeManager struct {
	path string
}

func NewResumeManager(path string) *ResumeManager {
	if path == "" {
		path = "./.filerename_resume.json"
	}
	return &ResumeManager{path: path}
}

func (rm *ResumeManager) Save(state *types.ResumeState) error {
	if state == nil { return nil }
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil { return err }
	return os.WriteFile(rm.path, data, 0644)
}

func (rm *ResumeManager) Load() (*types.ResumeState, error) {
	data, err := os.ReadFile(rm.path)
	if err != nil { return nil, err }
	var s types.ResumeState
	if err := json.Unmarshal(data, &s); err != nil { return nil, err }
	return &s, nil
}

func (rm *ResumeManager) Clear() error {
	if err := os.Remove(rm.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
