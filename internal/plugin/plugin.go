package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/filerename/filerename/pkg/types"
	lua "github.com/yuin/gopher-lua"
)

type LuaPlugin struct {
	Name   string
	Path   string
	Args   map[string]string
	state  *lua.LState
	mu     sync.Mutex
	loaded bool
}

type PluginManager struct {
	plugins []*LuaPlugin
	searchPaths []string
}

func NewPluginManager(paths []string) *PluginManager {
	return &PluginManager{
		plugins:     make([]*LuaPlugin, 0),
		searchPaths: paths,
	}
}

func (pm *PluginManager) LoadPlugins(configs []types.PluginConfig) error {
	for _, cfg := range configs {
		pluginPath := cfg.Path
		if !filepath.IsAbs(pluginPath) {
			found := false
			for _, sp := range pm.searchPaths {
				candidate := filepath.Join(sp, pluginPath)
				if _, err := checkFile(candidate); err == nil {
					pluginPath = candidate
					found = true
					break
				}
			}
			if !found {
				if _, err := checkFile(pluginPath); err != nil {
					continue
				}
			}
		}

		p := &LuaPlugin{
			Name:  strings.TrimSuffix(filepath.Base(pluginPath), filepath.Ext(pluginPath)),
			Path:  pluginPath,
			Args:  cfg.Args,
			state: lua.NewState(),
		}

		if err := p.load(); err != nil {
			return fmt.Errorf("load plugin %s: %w", pluginPath, err)
		}
		pm.plugins = append(pm.plugins, p)
	}
	return nil
}

func (pm *PluginManager) ApplyAll(f *types.FileInfo, index int) (string, bool, error) {
	name := f.Name
	changed := false
	for _, p := range pm.plugins {
		newName, ok, err := p.Apply(f, index, name)
		if err != nil {
			return name, changed, err
		}
		if ok {
			name = newName
			changed = true
		}
	}
	return name, changed, nil
}

func (pm *PluginManager) Close() {
	for _, p := range pm.plugins {
		p.Close()
	}
}

func (p *LuaPlugin) load() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.loaded {
		return nil
	}

	p.setupGlobals()

	if err := p.state.DoFile(p.Path); err != nil {
		return err
	}

	fn := p.state.GetGlobal("rename")
	if fn == lua.LNil {
		return fmt.Errorf("plugin %s: missing 'rename' function", p.Name)
	}
	if _, ok := fn.(*lua.LFunction); !ok {
		return fmt.Errorf("plugin %s: 'rename' must be a function", p.Name)
	}

	p.loaded = true
	return nil
}

func (p *LuaPlugin) setupGlobals() {
	L := p.state

	pluginTable := L.NewTable()
	pluginTable.RawSetString("name", lua.LString(p.Name))
	argsTable := L.NewTable()
	for k, v := range p.Args {
		argsTable.RawSetString(k, lua.LString(v))
	}
	pluginTable.RawSetString("args", argsTable)
	L.SetGlobal("plugin", pluginTable)

	helpers := L.NewTable()
	helpers.RawSetString("snake_case", L.NewFunction(luaSnakeCase))
	helpers.RawSetString("camel_case", L.NewFunction(luaCamelCase))
	helpers.RawSetString("pascal_case", L.NewFunction(luaPascalCase))
	helpers.RawSetString("kebab_case", L.NewFunction(luaKebabCase))
	helpers.RawSetString("pad_left", L.NewFunction(luaPadLeft))
	helpers.RawSetString("pad_right", L.NewFunction(luaPadRight))
	L.SetGlobal("helper", helpers)

	L.SetGlobal("log", L.NewFunction(func(L *lua.LState) int {
		msg := L.CheckString(1)
		fmt.Printf("[plugin %s] %s\n", p.Name, msg)
		return 0
	}))
}

func (p *LuaPlugin) Apply(f *types.FileInfo, index int, currentName string) (string, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.loaded {
		return currentName, false, nil
	}

	L := p.state
	fileTable := L.NewTable()
	fileTable.RawSetString("path", lua.LString(f.Path))
	fileTable.RawSetString("dir", lua.LString(f.Dir))
	fileTable.RawSetString("name", lua.LString(f.Name))
	fileTable.RawSetString("ext", lua.LString(f.Ext))
	fileTable.RawSetString("size", lua.LNumber(f.Size))
	fileTable.RawSetString("mod_time", lua.LNumber(f.ModTime.Unix()))
	fileTable.RawSetString("create_time", lua.LNumber(f.CreateTime.Unix()))
	if f.ExifDate != nil {
		fileTable.RawSetString("exif_date", lua.LNumber(f.ExifDate.Unix()))
	}
	if f.Hash != "" {
		fileTable.RawSetString("hash", lua.LString(f.Hash))
	}
	groupsTable := L.NewTable()
	for k, v := range f.MatchedGroups {
		groupsTable.RawSetString(k, lua.LString(v))
	}
	fileTable.RawSetString("groups", groupsTable)

	fn := L.GetGlobal("rename")
	err := L.CallByParam(lua.P{
		Fn:      fn,
		NRet:    2,
		Protect: true,
	}, fileTable, lua.LNumber(index), lua.LString(currentName))

	if err != nil {
		return currentName, false, fmt.Errorf("plugin %s error: %w", p.Name, err)
	}

	ret1 := L.Get(-2)
	ret2 := L.Get(-1)
	L.Pop(2)

	if ret1 == lua.LNil {
		return currentName, false, nil
	}

	result, ok := ret1.(lua.LString)
	if !ok {
		return currentName, false, fmt.Errorf("plugin %s: rename must return string", p.Name)
	}

	applied := false
	if b, ok := ret2.(lua.LBool); ok {
		applied = bool(b)
	} else {
		applied = string(result) != currentName
	}

	return string(result), applied, nil
}

func (p *LuaPlugin) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != nil {
		p.state.Close()
		p.state = nil
	}
}

func checkFile(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return !info.IsDir(), nil
}

func luaSnakeCase(L *lua.LState) int {
	s := L.CheckString(1)
	L.Push(lua.LString(toSnakeCaseLua(s)))
	return 1
}

func luaCamelCase(L *lua.LState) int {
	s := L.CheckString(1)
	L.Push(lua.LString(toCamelCaseLua(s)))
	return 1
}

func luaPascalCase(L *lua.LState) int {
	s := L.CheckString(1)
	L.Push(lua.LString(toPascalCaseLua(s)))
	return 1
}

func luaKebabCase(L *lua.LState) int {
	s := L.CheckString(1)
	L.Push(lua.LString(toKebabCaseLua(s)))
	return 1
}

func luaPadLeft(L *lua.LState) int {
	s := L.CheckString(1)
	w := L.CheckInt(2)
	p := L.OptString(3, "0")
	L.Push(lua.LString(padLeftLua(s, w, p)))
	return 1
}

func luaPadRight(L *lua.LState) int {
	s := L.CheckString(1)
	w := L.CheckInt(2)
	p := L.OptString(3, " ")
	L.Push(lua.LString(padRightLua(s, w, p)))
	return 1
}

func toSnakeCaseLua(s string) string {
	var result []rune
	for i, r := range s {
		if i > 0 && isUpperLua(r) && (i+1 < len([]rune(s)) && !isUpperLua([]rune(s)[i+1]) || isLowerLua([]rune(s)[i-1])) {
			result = append(result, '_')
		}
		if r == ' ' || r == '-' || r == '.' {
			result = append(result, '_')
		} else {
			result = append(result, toLowerRune(r))
		}
	}
	return string(result)
}

func toCamelCaseLua(s string) string {
	snake := toSnakeCaseLua(s)
	parts := strings.Split(snake, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			r := []rune(parts[i])
			r[0] = toUpperRune(r[0])
			parts[i] = string(r)
		}
	}
	return strings.Join(parts, "")
}

func toPascalCaseLua(s string) string {
	camel := toCamelCaseLua(s)
	if camel == "" { return "" }
	r := []rune(camel)
	r[0] = toUpperRune(r[0])
	return string(r)
}

func toKebabCaseLua(s string) string {
	return strings.ReplaceAll(toSnakeCaseLua(s), "_", "-")
}

func padLeftLua(s string, w int, p string) string {
	for len(s) < w { s = p + s }
	return s
}

func padRightLua(s string, w int, p string) string {
	for len(s) < w { s = s + p }
	return s
}

func isUpperLua(r rune) bool { return r >= 'A' && r <= 'Z' }
func isLowerLua(r rune) bool { return r >= 'a' && r <= 'z' }
func toUpperRune(r rune) rune {
	if isLowerLua(r) { return r - 32 }
	return r
}
func toLowerRune(r rune) rune {
	if isUpperLua(r) { return r + 32 }
	return r
}
