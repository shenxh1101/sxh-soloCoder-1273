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
	cfg         *config.Config
	rules       []types.Rule
	dedup       *rules.DedupManager
	classifier  *rules.Classifier
	plugins     *plugin.PluginManager
	progress    ProgressReporter

	resumeTaskID  string
	resumeEnabled bool
	resumePersist bool

	resumeMu      sync.Mutex
	resumeState   *types.ResumeState
	resumeRM      *ResumeManager
	lastSaveTime  time.Time
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
	Actions         []*types.RenameAction
	Total           int
	Succeeded       int
	Failed          int
	Skipped         int
	Elapsed         time.Duration
	HistoryID       string
	DuplicateGroups map[string][]*types.FileInfo
	Warnings        []string
	Interrupted     bool
}

func NewExecutor(cfg *config.Config) *Executor {
	return &Executor{
		cfg: cfg,
		resumeEnabled: cfg.Global.Resume,
		resumePersist: true,
	}
}

func (e *Executor) WithProgress(p ProgressReporter) *Executor {
	e.progress = p
	return e
}

func (e *Executor) SetResumeInfo(taskID string, enabled bool, persist bool) {
	e.resumeTaskID = taskID
	e.resumeEnabled = enabled
	e.resumePersist = persist
	if enabled {
		e.resumeRM = NewResumeManager("")
	}
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
	currentName := f.Name
	appliedAny := false
	var steps []types.RuleStep

	for _, rule := range e.rules {
		before := currentName
		result, applied, err := rule.Apply(f, index)
		if err == nil && applied {
			currentName = result
			appliedAny = true
		}
		steps = append(steps, types.RuleStep{
			Rule:    rule.Name(),
			Before:  before,
			After:   currentName,
			Applied: err == nil && applied,
		})
	}

	if e.plugins != nil {
		before := currentName
		result, applied, err := e.plugins.ApplyAll(f, index, currentName)
		if err == nil && applied {
			currentName = result
			appliedAny = true
		}
		steps = append(steps, types.RuleStep{
			Rule:    "plugins",
			Before:  before,
			After:   currentName,
			Applied: err == nil && applied,
		})
	}

	targetDir := f.Dir
	classifyApplied := false
	if e.classifier != nil {
		if dir, ok := e.classifier.Classify(f); ok {
			targetDir = dir
			appliedAny = true
			classifyApplied = true
		}
	}
	steps = append(steps, types.RuleStep{
		Rule:    "classify",
		Before:  f.Dir,
		After:   targetDir,
		Applied: classifyApplied,
	})

	if !appliedAny {
		return nil, true
	}

	if currentName == "" {
		currentName = f.Name
	}

	targetPath := filepath.Join(targetDir, currentName+f.Ext)
	beforeDedup := targetPath
	dedupApplied := false
	if e.dedup != nil {
		resolved := e.dedup.ResolveConflict(targetPath)
		if resolved != targetPath && resolved != "" {
			targetPath = resolved
			dedupApplied = true
		}
		if resolved == "" {
			return nil, true
		}
	}
	steps = append(steps, types.RuleStep{
		Rule:    "dedup",
		Before:  beforeDedup,
		After:   targetPath,
		Applied: dedupApplied,
	})

	if targetPath == f.Path {
		return nil, true
	}

	action := &types.RenameAction{
		SourcePath: f.Path,
		TargetPath: targetPath,
		SourceFile: *f,
		Operation:  types.OpRename,
		Timestamp:  time.Now(),
		Steps:      steps,
	}
	if targetDir != f.Dir {
		action.Operation = types.OpMove
	}
	return action, false
}

func (e *Executor) saveResumeCheckpoint(force bool) {
	if !e.resumeEnabled || !e.resumePersist || e.resumeState == nil || e.resumeRM == nil {
		return
	}
	e.resumeMu.Lock()
	defer e.resumeMu.Unlock()

	now := time.Now()
	if !force && now.Sub(e.lastSaveTime) < 500*time.Millisecond {
		return
	}
	e.lastSaveTime = now
	e.resumeState.LastUpdate = now
	_ = e.resumeRM.Save(e.resumeState)
}

func (e *Executor) Execute(ctx context.Context, actions []*types.RenameAction) (*ExecutorResult, error) {
	startTime := time.Now()
	result := &ExecutorResult{
		Actions: actions,
		Total:   len(actions),
	}

	if len(actions) == 0 {
		return result, nil
	}

	workers := e.cfg.Global.Workers
	if workers <= 0 { workers = 32 }
	dryRun := e.cfg.Global.DryRun || e.cfg.Global.Preview

	if e.resumeEnabled && e.resumePersist && !dryRun {
		e.resumeMu.Lock()
		initProcessed := make([]string, 0, len(actions))
		e.resumeState = &types.ResumeState{
			TaskID:     e.resumeTaskID,
			Progress:   0,
			Total:      len(actions),
			Processed:  initProcessed,
			Failed:     make([]string, 0),
			LastUpdate: time.Now(),
		}
		e.resumeMu.Unlock()
		e.saveResumeCheckpoint(true)
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

	if e.progress != nil {
		e.progress.SetPhase("Executing")
		e.progress.Reset()
		e.progress.SetTotal(len(actions))
	}

	var (
		wg       sync.WaitGroup
		sem      = make(chan struct{}, workers)
		mu       sync.Mutex
		succ     int32
		fail     int32
		skip     int32
		interrupted int32
	)

	saveTrigger := make(chan struct{}, 1)
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				e.saveResumeCheckpoint(false)
			case <-saveTrigger:
				e.saveResumeCheckpoint(true)
				return
			case <-ctx.Done():
				e.saveResumeCheckpoint(true)
				return
			}
		}
	}()

	for _, action := range actions {
		select {
		case <-ctx.Done():
			atomic.StoreInt32(&interrupted, 1)
			break
		default:
		}
		if atomic.LoadInt32(&interrupted) == 1 {
			break
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
				atomic.AddInt32(&succ, 1)
				if e.progress != nil { e.progress.Increment(1) }
				if e.resumeEnabled && e.resumePersist {
					e.resumeMu.Lock()
					if e.resumeState != nil {
						e.resumeState.Processed = append(e.resumeState.Processed, a.SourcePath)
						e.resumeState.Progress++
					}
					e.resumeMu.Unlock()
				}
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
				if e.resumeState != nil {
					e.resumeState.Failed = append(e.resumeState.Failed, a.SourcePath)
					e.resumeState.Progress++
				}
				mu.Unlock()
				if e.progress != nil { e.progress.Increment(1) }
				return
			}

			if _, err := os.Stat(a.TargetPath); err == nil {
				mu.Lock()
				a.Success = false
				a.Error = "target already exists"
				atomic.AddInt32(&skip, 1)
				if e.resumeState != nil {
					e.resumeState.Processed = append(e.resumeState.Processed, a.SourcePath)
					e.resumeState.Progress++
				}
				mu.Unlock()
				if e.progress != nil { e.progress.Increment(1) }
				return
			}

			if _, err := os.Stat(a.SourcePath); os.IsNotExist(err) {
				mu.Lock()
				a.Success = false
				a.Error = "source not found (already processed?)"
				atomic.AddInt32(&skip, 1)
				if e.resumeState != nil {
					e.resumeState.Processed = append(e.resumeState.Processed, a.SourcePath)
					e.resumeState.Progress++
				}
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
				if e.resumeState != nil {
					e.resumeState.Failed = append(e.resumeState.Failed, a.SourcePath)
					e.resumeState.Progress++
				}
				mu.Unlock()
			} else {
				a.Executed = true
				a.Success = true
				atomic.AddInt32(&succ, 1)
				e.resumeMu.Lock()
				if e.resumeState != nil {
					e.resumeState.Processed = append(e.resumeState.Processed, a.SourcePath)
					e.resumeState.Progress++
				}
				e.resumeMu.Unlock()
			}
			if e.progress != nil { e.progress.Increment(1) }
		}()
	}
	wg.Wait()

	close(saveTrigger)
	time.Sleep(100 * time.Millisecond)
	e.saveResumeCheckpoint(true)

	result.Succeeded = int(atomic.LoadInt32(&succ))
	result.Failed = int(atomic.LoadInt32(&fail))
	result.Skipped = int(atomic.LoadInt32(&skip))
	result.Interrupted = atomic.LoadInt32(&interrupted) == 1
	result.Elapsed = time.Since(startTime)
	if e.progress != nil { e.progress.Finish() }

	if !dryRun && result.Succeeded > 0 {
		hm, err := history.NewHistoryManager(e.cfg.Global.HistoryFile, e.cfg.Global.HistoryLimit)
		if err == nil {
			successActions := make([]*types.RenameAction, 0, result.Succeeded)
			for _, a := range actions {
				if a.Success && a.Executed {
					successActions = append(successActions, a)
				}
			}
			if len(successActions) > 0 {
				id, err := hm.Record(successActions)
				if err == nil {
					result.HistoryID = id
				}
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
	dir := filepath.Dir(rm.path)
	if dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil { return err }
	tmp := rm.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, rm.path)
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
	tmp := rm.path + ".tmp"
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
