package scanner

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/filerename/filerename/pkg/types"
)

type Scanner struct {
	RootDirs    []string
	Recursive   bool
	Patterns    []string
	ExcludePatterns []string
	IncludeExt  []string
	ExcludeExt  []string
	Regex       *regexp.Regexp
	ExcludeRegex *regexp.Regexp
	WorkerCount int
	Count       int64
}

func NewScanner(rootDirs []string) *Scanner {
	return &Scanner{
		RootDirs:    rootDirs,
		Recursive:   true,
		WorkerCount: 16,
	}
}

func (s *Scanner) WithRecursive(r bool) *Scanner { s.Recursive = r; return s }
func (s *Scanner) WithPatterns(p []string) *Scanner { s.Patterns = p; return s }
func (s *Scanner) WithExcludePatterns(p []string) *Scanner { s.ExcludePatterns = p; return s }
func (s *Scanner) WithIncludeExt(e []string) *Scanner { s.IncludeExt = e; return s }
func (s *Scanner) WithExcludeExt(e []string) *Scanner { s.ExcludeExt = e; return s }
func (s *Scanner) WithRegex(pattern string) *Scanner {
	if pattern != "" {
		s.Regex = regexp.MustCompile(pattern)
	}
	return s
}
func (s *Scanner) WithExcludeRegex(pattern string) *Scanner {
	if pattern != "" {
		s.ExcludeRegex = regexp.MustCompile(pattern)
	}
	return s
}
func (s *Scanner) WithWorkers(n int) *Scanner { s.WorkerCount = n; return s }

func (s *Scanner) Scan() ([]*types.FileInfo, error) {
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		results  = make([]*types.FileInfo, 0, 1024)
		sem      = make(chan struct{}, s.WorkerCount)
		atomicCount int64
	)

	walkFn := func(root string) error {
		return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}

			if d.IsDir() {
				if !s.Recursive && path != root {
					return fs.SkipDir
				}
				if s.shouldExcludeDir(path) {
					return fs.SkipDir
				}
				return nil
			}

			wg.Add(1)
			sem <- struct{}{}
			go func(p string, entry fs.DirEntry) {
				defer wg.Done()
				defer func() { <-sem }()

				fi, ok := s.processFile(p, entry)
				if ok && fi != nil {
					mu.Lock()
					results = append(results, fi)
					mu.Unlock()
					atomic.AddInt64(&atomicCount, 1)
				}
			}(path, d)

			return nil
		})
	}

	for _, root := range s.RootDirs {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		if err := walkFn(absRoot); err != nil {
			return nil, err
		}
	}

	wg.Wait()
	s.Count = atomicCount
	return results, nil
}

func (s *Scanner) ScanFast(fileChan chan<- *types.FileInfo, done chan<- error) {
	var (
		wg     sync.WaitGroup
		sem    = make(chan struct{}, s.WorkerCount)
		closed bool
		mu     sync.Mutex
	)

	closeDone := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			closed = true
			done <- err
			close(done)
			close(fileChan)
		}
	}

	walkFn := func(root string) error {
		return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if !s.Recursive && path != root {
					return fs.SkipDir
				}
				if s.shouldExcludeDir(path) {
					return fs.SkipDir
				}
				return nil
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(p string, entry fs.DirEntry) {
				defer wg.Done()
				defer func() { <-sem }()
				fi, ok := s.processFile(p, entry)
				if ok && fi != nil {
					fileChan <- fi
				}
			}(path, d)
			return nil
		})
	}

	go func() {
		var firstErr error
		for _, root := range s.RootDirs {
			absRoot, err := filepath.Abs(root)
			if err != nil {
				firstErr = err
				break
			}
			if err := walkFn(absRoot); err != nil {
				firstErr = err
				break
			}
		}
		wg.Wait()
		closeDone(firstErr)
	}()
}

func (s *Scanner) processFile(path string, d fs.DirEntry) (*types.FileInfo, bool) {
	name := d.Name()

	if !s.matchPatterns(name, path) {
		return nil, false
	}

	ext := strings.ToLower(filepath.Ext(name))
	if len(s.IncludeExt) > 0 {
		matched := false
		for _, e := range s.IncludeExt {
			if ext == strings.ToLower(e) { matched = true; break }
		}
		if !matched { return nil, false }
	}
	for _, e := range s.ExcludeExt {
		if ext == strings.ToLower(e) { return nil, false }
	}

	info, err := d.Info()
	if err != nil {
		return nil, false
	}

	var createTime time.Time
	if stat, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		createTime = time.Unix(0, stat.CreationTime.Nanoseconds())
	}

	fi := &types.FileInfo{
		Path:       path,
		Dir:        filepath.Dir(path),
		Name:       strings.TrimSuffix(name, ext),
		Ext:        ext,
		Size:       info.Size(),
		ModTime:    info.ModTime(),
		CreateTime: createTime,
		Mode:       info.Mode(),
		IsDir:      info.IsDir(),
		MatchedGroups: make(map[string]string),
	}

	if s.Regex != nil {
		if matches := s.Regex.FindStringSubmatch(name); matches != nil {
			for i, name := range s.Regex.SubexpNames() {
				if i > 0 && name != "" && i < len(matches) {
					fi.MatchedGroups[name] = matches[i]
				}
			}
		}
	}

	return fi, true
}

func (s *Scanner) matchPatterns(name, path string) bool {
	if len(s.Patterns) == 0 && s.Regex == nil {
		return true
	}
	for _, p := range s.Patterns {
		matched, err := filepath.Match(p, name)
		if err == nil && matched {
			return true
		}
	}
	if s.Regex != nil && s.Regex.MatchString(name) {
		return true
	}
	return false
}

func (s *Scanner) shouldExcludeDir(path string) bool {
	base := filepath.Base(path)
	excludedDirs := []string{".git", "node_modules", ".svn", ".hg", "$RECYCLE.BIN", "System Volume Information"}
	for _, d := range excludedDirs {
		if strings.EqualFold(base, d) {
			return true
		}
	}
	if s.ExcludeRegex != nil && s.ExcludeRegex.MatchString(path) {
		return true
	}
	return false
}

func GetCreationTime(info os.FileInfo) time.Time {
	if stat, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return time.Unix(0, stat.CreationTime.Nanoseconds())
	}
	return info.ModTime()
}
