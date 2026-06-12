package history

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/filerename/filerename/pkg/types"
	"github.com/google/uuid"
)

type HistoryManager struct {
	filePath string
	limit    int
	mu       sync.Mutex
	entries  []*types.HistoryEntry
}

func NewHistoryManager(filePath string, limit int) (*HistoryManager, error) {
	if filePath == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			filePath = filepath.Join(home, ".filerename", "history.json")
		}
	}
	if strings.HasPrefix(filePath, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			filePath = filepath.Join(home, strings.TrimPrefix(filePath, "~"))
		}
	}
	if limit <= 0 {
		limit = 100
	}

	hm := &HistoryManager{
		filePath: filePath,
		limit:    limit,
		entries:  make([]*types.HistoryEntry, 0),
	}

	if err := hm.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return hm, nil
}

func (hm *HistoryManager) load() error {
	if err := os.MkdirAll(filepath.Dir(hm.filePath), 0755); err != nil {
		return err
	}
	f, err := os.Open(hm.filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, &hm.entries)
}

func (hm *HistoryManager) save() error {
	if err := os.MkdirAll(filepath.Dir(hm.filePath), 0755); err != nil {
		return err
	}

	sort.Slice(hm.entries, func(i, j int) bool {
		return hm.entries[i].Timestamp.After(hm.entries[j].Timestamp)
	})

	if len(hm.entries) > hm.limit {
		hm.entries = hm.entries[:hm.limit]
	}

	data, err := json.MarshalIndent(hm.entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := hm.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, hm.filePath)
}

func (hm *HistoryManager) Record(actions []*types.RenameAction) (string, error) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	successCount := 0
	for _, a := range actions {
		if a.Success {
			successCount++
		}
	}

	id := generateID()
	summary := fmt.Sprintf("%d files, %d succeeded", len(actions), successCount)
	checksum := computeChecksum(actions)

	realActions := make([]types.RenameAction, 0, len(actions))
	for _, a := range actions {
		realActions = append(realActions, *a)
	}

	entry := &types.HistoryEntry{
		ID:        id,
		Timestamp: time.Now(),
		Actions:   realActions,
		Summary:   summary,
		Checksum:  checksum,
	}

	hm.entries = append([]*types.HistoryEntry{entry}, hm.entries...)
	if err := hm.save(); err != nil {
		return "", err
	}
	return id, nil
}

func (hm *HistoryManager) List(count int) []*types.HistoryEntry {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if count <= 0 || count > len(hm.entries) {
		count = len(hm.entries)
	}
	result := make([]*types.HistoryEntry, count)
	copy(result, hm.entries[:count])
	return result
}

func (hm *HistoryManager) Get(id string) (*types.HistoryEntry, error) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	for _, e := range hm.entries {
		if e.ID == id {
			return e, nil
		}
	}
	return nil, fmt.Errorf("history entry not found: %s", id)
}

func (hm *HistoryManager) Undo(id string, workers int, dryRun bool) (int, int, error) {
	entry, err := hm.Get(id)
	if err != nil {
		return 0, 0, err
	}

	success, failed := 0, 0
	if workers <= 0 { workers = 16 }
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i := len(entry.Actions) - 1; i >= 0; i-- {
		action := entry.Actions[i]
		if !action.Success {
			continue
		}
		if action.Operation != types.OpRename && action.Operation != types.OpMove {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(a types.RenameAction) {
			defer wg.Done()
			defer func() { <-sem }()

			if dryRun {
				mu.Lock()
				success++
				mu.Unlock()
				return
			}

			if _, err := os.Stat(a.TargetPath); err == nil {
				if _, err := os.Stat(a.SourcePath); os.IsNotExist(err) {
					if err := os.MkdirAll(filepath.Dir(a.SourcePath), 0755); err != nil {
						mu.Lock()
						failed++
						mu.Unlock()
						return
					}
					if err := os.Rename(a.TargetPath, a.SourcePath); err != nil {
						mu.Lock()
						failed++
						mu.Unlock()
						return
					}
					mu.Lock()
					success++
					mu.Unlock()
					return
				}
			}
			mu.Lock()
			failed++
			mu.Unlock()
		}(action)
	}
	wg.Wait()

	hm.removeEntry(id)
	return success, failed, nil
}

func (hm *HistoryManager) UndoLast(n int, workers int, dryRun bool) (int, int, error) {
	hm.mu.Lock()
	entries := make([]*types.HistoryEntry, 0)
	for i, e := range hm.entries {
		if i < n {
			entries = append(entries, e)
		}
	}
	hm.mu.Unlock()

	totalSuccess, totalFailed := 0, 0
	for _, e := range entries {
		s, f, err := hm.Undo(e.ID, workers, dryRun)
		if err == nil {
			totalSuccess += s
			totalFailed += f
		}
	}
	return totalSuccess, totalFailed, nil
}

func (hm *HistoryManager) removeEntry(id string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	result := make([]*types.HistoryEntry, 0, len(hm.entries))
	for _, e := range hm.entries {
		if e.ID != id {
			result = append(result, e)
		}
	}
	hm.entries = result
	_ = hm.save()
}

func (hm *HistoryManager) Clear() error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	hm.entries = make([]*types.HistoryEntry, 0)
	if err := os.Remove(hm.filePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func generateID() string {
	return uuid.New().String()[:12]
}

func computeChecksum(actions []*types.RenameAction) string {
	h := sha256.New()
	for _, a := range actions {
		h.Write([]byte(a.SourcePath))
		h.Write([]byte(a.TargetPath))
		h.Write([]byte{byte(a.Timestamp.Unix() % 256)})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
