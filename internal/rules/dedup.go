package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/filerename/filerename/pkg/types"
)

type DedupManager struct {
	cfg *types.DedupConfig
	seen map[string]int
	mu sync.Mutex
}

func NewDedupManager(cfg *types.DedupConfig) *DedupManager {
	if cfg.Strategy == "" { cfg.Strategy = "suffix" }
	if cfg.Suffix == "" { cfg.Suffix = "_copy" }
	if cfg.ConflictDir == "" { cfg.ConflictDir = "_conflicts" }
	return &DedupManager{
		cfg:  cfg,
		seen: make(map[string]int),
	}
}

func (d *DedupManager) separator() string {
	return "_"
}

func (d *DedupManager) ResolveConflict(targetPath string) string {
	if !d.cfg.Enabled {
		return targetPath
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	dir := filepath.Dir(targetPath)
	name := strings.TrimSuffix(filepath.Base(targetPath), filepath.Ext(targetPath))
	ext := filepath.Ext(targetPath)
	key := strings.ToLower(targetPath)

	count, exists := d.seen[key]
	if !exists {
		if _, err := os.Stat(targetPath); err == nil {
			exists = true
			count = 1
		} else {
			d.seen[key] = 0
			return targetPath
		}
	}

	if !exists {
		d.seen[key] = 0
		return targetPath
	}

	switch strings.ToLower(d.cfg.Strategy) {
	case "conflict_dir", "dir", "folder":
		conflictDir := filepath.Join(dir, d.cfg.ConflictDir)
		newPath := filepath.Join(conflictDir, filepath.Base(targetPath))
		d.seen[key]++
		return d.resolveInDir(newPath)
	case "skip":
		return ""
	default:
		count++
		d.seen[key] = count
		newName := fmt.Sprintf("%s%s%s%d%s", name, d.separator(), d.cfg.Suffix, count, ext)
		newPath := filepath.Join(dir, newName)
		for {
			if _, err := os.Stat(newPath); os.IsNotExist(err) {
				k := strings.ToLower(newPath)
				d.seen[k] = 0
				return newPath
			}
			count++
			d.seen[key] = count
			newName = fmt.Sprintf("%s%s%s%d%s", name, d.separator(), d.cfg.Suffix, count, ext)
			newPath = filepath.Join(dir, newName)
		}
	}
}

func (d *DedupManager) resolveInDir(path string) string {
	dir := filepath.Dir(path)
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	ext := filepath.Ext(path)
	count := 1
	for {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path
		}
		count++
		path = filepath.Join(dir, fmt.Sprintf("%s_%d%s", name, count, ext))
	}
}

type Classifier struct {
	cfg       *types.ClassifyConfig
	categories map[string][]string
}

var DefaultCategories = map[string][]string{
	"images":     {".jpg", ".jpeg", ".png", ".gif", ".bmp", ".tiff", ".tif", ".webp", ".heic", ".heif", ".raw", ".cr2", ".cr3", ".nef", ".arw", ".dng", ".svg", ".ico"},
	"videos":     {".mp4", ".avi", ".mkv", ".mov", ".wmv", ".flv", ".webm", ".m4v", ".mpg", ".mpeg", ".3gp", ".vob", ".ts"},
	"audio":      {".mp3", ".wav", ".flac", ".aac", ".ogg", ".wma", ".m4a", ".opus", ".aiff", ".ape"},
	"documents":  {".pdf", ".doc", ".docx", ".txt", ".rtf", ".odt", ".xls", ".xlsx", ".ppt", ".pptx", ".csv", ".ods", ".odp", ".md", ".epub", ".mobi"},
	"archives":   {".zip", ".rar", ".7z", ".tar", ".gz", ".bz2", ".xz", ".iso", ".cab", ".tgz"},
	"code":       {".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".java", ".c", ".cpp", ".h", ".hpp", ".cs", ".rb", ".php", ".rs", ".swift", ".kt", ".scala", ".html", ".css", ".scss", ".sass", ".json", ".xml", ".yaml", ".yml", ".sql", ".sh", ".bat", ".ps1"},
	"executables":{".exe", ".msi", ".apk", ".app", ".deb", ".rpm", ".dmg", ".pkg"},
	"fonts":      {".ttf", ".otf", ".woff", ".woff2", ".eot"},
}

func NewClassifier(cfg *types.ClassifyConfig) *Classifier {
	c := &Classifier{
		cfg:       cfg,
		categories: make(map[string][]string),
	}
	if cfg.Presets == nil || len(cfg.Presets) == 0 {
		for k, v := range DefaultCategories {
			c.categories[k] = v
		}
	} else {
		for _, preset := range cfg.Presets {
			if v, ok := DefaultCategories[preset]; ok {
				c.categories[preset] = v
			}
		}
	}
	for folder, patterns := range cfg.Custom {
		exts := strings.Split(patterns, ",")
		for i, e := range exts {
			e = strings.TrimSpace(e)
			if !strings.HasPrefix(e, ".") {
				e = "." + e
			}
			exts[i] = strings.ToLower(e)
		}
		c.categories[folder] = append(c.categories[folder], exts...)
	}
	return c
}

func (c *Classifier) Classify(f *types.FileInfo) (string, bool) {
	if !c.cfg.Enabled {
		return "", false
	}
	ext := strings.ToLower(f.Ext)
	if ext == "" {
		return "", false
	}

	var matchedFolder string
	for folder, exts := range c.categories {
		for _, e := range exts {
			if e == ext {
				matchedFolder = folder
				break
			}
		}
		if matchedFolder != "" {
			break
		}
	}

	if matchedFolder == "" {
		matchedFolder = "others"
	}

	baseDir := f.Dir
	if c.cfg.BaseDir != "" {
		if filepath.IsAbs(c.cfg.BaseDir) {
			baseDir = c.cfg.BaseDir
		} else {
			baseDir = filepath.Join(f.Dir, c.cfg.BaseDir)
		}
	}
	return filepath.Join(baseDir, matchedFolder), true
}

func (c *Classifier) GetDirsToCreate() []string {
	dirs := make([]string, 0, len(c.categories)+1)
	for folder := range c.categories {
		dirs = append(dirs, folder)
	}
	dirs = append(dirs, "others")
	return dirs
}

type HashDeduper struct {
	cfg     *types.HashDedupConfig
}

func NewHashDeduper(cfg *types.HashDedupConfig) *HashDeduper {
	if cfg.Algorithm == "" { cfg.Algorithm = "sha256" }
	if cfg.KeepStrategy == "" { cfg.KeepStrategy = "oldest" }
	if cfg.DeleteAction == "" { cfg.DeleteAction = "trash" }
	return &HashDeduper{cfg: cfg}
}

func (h *HashDeduper) FindDuplicates(files []*types.FileInfo, workers int) map[string][]*types.FileInfo {
	if workers <= 0 {
		workers = 16
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		sem     = make(chan struct{}, workers)
		hashMap = make(map[string][]*types.FileInfo)
	)

	for _, f := range files {
		if f.Size == 0 {
			continue
		}
		if h.cfg.MinSize > 0 && f.Size < h.cfg.MinSize {
			continue
		}
		if h.cfg.MaxSize > 0 && f.Size > h.cfg.MaxSize {
			continue
		}
		if len(h.cfg.Extensions) > 0 {
			matched := false
			ext := strings.ToLower(f.Ext)
			for _, e := range h.cfg.Extensions {
				if strings.ToLower(e) == ext { matched = true; break }
			}
			if !matched { continue }
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(file *types.FileInfo) {
			defer wg.Done()
			defer func() { <-sem }()

			hash, err := computeFileHash(file.Path)
			if err != nil {
				return
			}
			file.Hash = hash

			mu.Lock()
			hashMap[hash] = append(hashMap[hash], file)
			mu.Unlock()
		}(f)
	}
	wg.Wait()

	dupes := make(map[string][]*types.FileInfo)
	for hash, list := range hashMap {
		if len(list) > 1 {
			dupes[hash] = list
		}
	}
	return dupes
}

func computeFileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	const bufSize = 1024 * 1024
	buf := make([]byte, bufSize)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (h *HashDeduper) GetKeepAndDelete(dupes []*types.FileInfo) (keep *types.FileInfo, delete []*types.FileInfo) {
	if len(dupes) < 2 {
		return nil, nil
	}
	sorted := make([]*types.FileInfo, len(dupes))
	copy(sorted, dupes)

	switch strings.ToLower(h.cfg.KeepStrategy) {
	case "newest":
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].ModTime.After(sorted[j].ModTime)
		})
	case "largest":
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Size > sorted[j].Size
		})
	case "shortest_name":
		sort.Slice(sorted, func(i, j int) bool {
			return len(sorted[i].Name) < len(sorted[j].Name)
		})
	case "original", "first":
	default:
		sort.Slice(sorted, func(i, j int) bool {
			a := sorted[i].ModTime
			b := sorted[j].ModTime
			if !sorted[i].CreateTime.IsZero() {
				a = sorted[i].CreateTime
			}
			if !sorted[j].CreateTime.IsZero() {
				b = sorted[j].CreateTime
			}
			return a.Before(b)
		})
	}
	return sorted[0], sorted[1:]
}

func FastDuplicateDetection(files []*types.FileInfo, workers int) map[string][]*types.FileInfo {
	if workers <= 0 { workers = 16 }

	sizeMap := make(map[int64][]*types.FileInfo)
	for _, f := range files {
		if f.Size > 0 {
			sizeMap[f.Size] = append(sizeMap[f.Size], f)
		}
	}

	potential := make([]*types.FileInfo, 0)
	for _, list := range sizeMap {
		if len(list) > 1 {
			potential = append(potential, list...)
		}
	}

	cfg := &types.HashDedupConfig{Algorithm: "sha256"}
	return NewHashDeduper(cfg).FindDuplicates(potential, workers)
}
