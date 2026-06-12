package rules

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/filerename/filerename/pkg/types"
)

type SequenceRule struct {
	cfg *types.SequenceConfig
}

func NewSequenceRule(cfg *types.SequenceConfig) *SequenceRule {
	if cfg.PadChar == "" { cfg.PadChar = "0" }
	if cfg.Separator == "" { cfg.Separator = "_" }
	if cfg.Position == "" { cfg.Position = "suffix" }
	return &SequenceRule{cfg: cfg}
}

func (r *SequenceRule) Name() string { return "sequence" }

func (r *SequenceRule) Apply(f *types.FileInfo, index int) (string, bool, error) {
	if !r.cfg.Enabled {
		return "", false, nil
	}
	num := r.cfg.Start + index*r.cfg.Step
	width := r.cfg.Width
	if width == 0 {
		width = len(strconv.Itoa(num))
	}
	seq := padLeft(strconv.Itoa(num), width, r.cfg.PadChar)
	if r.cfg.Format != "" {
		seq = fmt.Sprintf(r.cfg.Format, num)
	}

	base := f.Name
	switch r.cfg.Position {
	case "prefix":
		return seq + r.cfg.Separator + base, true, nil
	case "both":
		return seq + r.cfg.Separator + base + r.cfg.Separator + seq, true, nil
	default:
		return base + r.cfg.Separator + seq, true, nil
	}
}

type CaseRule struct {
	cfg *types.CaseConfig
}

func NewCaseRule(cfg *types.CaseConfig) *CaseRule {
	if cfg.Target == "" { cfg.Target = "snake_case" }
	return &CaseRule{cfg: cfg}
}

func (r *CaseRule) Name() string { return "case" }

func (r *CaseRule) Apply(f *types.FileInfo, index int) (string, bool, error) {
	if !r.cfg.Enabled {
		return "", false, nil
	}
	name := f.Name
	if r.cfg.AutoDetect {
		name = splitCaseTokens(name)
	}
	var result string
	switch strings.ToLower(r.cfg.Target) {
	case "snake_case", "snake":
		result = toSnakeCase(name)
	case "kebab-case", "kebab", "dash":
		result = toKebabCase(name)
	case "camelcase", "camel":
		result = toCamelCase(name)
	case "pascalcase", "pascal":
		result = toPascalCase(name)
	case "upper", "uppercase":
		result = strings.ToUpper(name)
	case "lower", "lowercase":
		result = strings.ToLower(name)
	case "title":
		result = toTitleCase(name)
	default:
		result = toSnakeCase(name)
	}
	return result, true, nil
}

type RegexRule struct {
	cfg *types.RegexConfig
	re  *regexp.Regexp
}

func NewRegexRule(cfg *types.RegexConfig) (*RegexRule, error) {
	re, err := regexp.Compile(cfg.Pattern)
	if err != nil {
		return nil, err
	}
	return &RegexRule{cfg: cfg, re: re}, nil
}

func (r *RegexRule) Name() string { return "regex" }

func (r *RegexRule) Apply(f *types.FileInfo, index int) (string, bool, error) {
	if !r.cfg.Enabled {
		return "", false, nil
	}
	name := f.Name + f.Ext
	if !r.re.MatchString(name) && !r.re.MatchString(f.Name) {
		return "", false, nil
	}

	result := f.Name
	if r.cfg.Replace != "" {
		result = r.re.ReplaceAllString(f.Name, r.cfg.Replace)
	}
	for gname, replacement := range r.cfg.Groups {
		if val, ok := f.MatchedGroups[gname]; ok {
			result = strings.ReplaceAll(result, "${"+gname+"}", val)
			result = strings.ReplaceAll(result, "%{"+gname+"}", val)
			_ = replacement
		}
	}
	_ = name
	return result, true, nil
}

func padLeft(s string, n int, pad string) string {
	if len(s) >= n {
		return s
	}
	padStr := strings.Repeat(pad, n-len(s))
	return padStr + s
}

func splitCaseTokens(s string) string {
	var b strings.Builder
	prevLower := false
	prevNum := false
	for _, r := range s {
		curLower := unicode.IsLower(r)
		curNum := unicode.IsDigit(r)
		isSep := r == '_' || r == '-' || r == ' ' || r == '.'
		if isSep {
			b.WriteRune(' ')
			prevLower = false
			prevNum = false
			continue
		}
		if prevLower && unicode.IsUpper(r) {
			b.WriteRune(' ')
		} else if unicode.IsUpper(r) && prevLower {
			b.WriteRune(' ')
		} else if prevNum != curNum && !isSep {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
		prevLower = curLower
		prevNum = curNum
	}
	return strings.TrimSpace(b.String())
}

func toSnakeCase(s string) string {
	tokens := splitTokens(s)
	return strings.ToLower(strings.Join(tokens, "_"))
}

func toKebabCase(s string) string {
	tokens := splitTokens(s)
	return strings.ToLower(strings.Join(tokens, "-"))
}

func toCamelCase(s string) string {
	tokens := splitTokens(s)
	if len(tokens) == 0 { return "" }
	result := strings.ToLower(tokens[0])
	for _, t := range tokens[1:] {
		if t == "" { continue }
		result += strings.ToUpper(t[:1]) + strings.ToLower(t[1:])
	}
	return result
}

func toPascalCase(s string) string {
	tokens := splitTokens(s)
	var result string
	for _, t := range tokens {
		if t == "" { continue }
		result += strings.ToUpper(t[:1]) + strings.ToLower(t[1:])
	}
	return result
}

func toTitleCase(s string) string {
	tokens := splitTokens(s)
	for i, t := range tokens {
		if t == "" { continue }
		tokens[i] = strings.ToUpper(t[:1]) + strings.ToLower(t[1:])
	}
	return strings.Join(tokens, " ")
}

func splitTokens(s string) []string {
	s = splitCaseTokens(s)
	re := regexp.MustCompile(`[\s_\-\.\,\+\=\@\#\$\%\^\&\*\(\)\[\]\{\}\;\:\'\"\<\>\?\/\\\|]+`)
	parts := re.Split(s, -1)
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
