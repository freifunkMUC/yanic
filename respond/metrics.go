package respond

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

// Defaults and hard ceilings for [respondd.metrics]. Respondd data is
// untrusted input from any node in the mesh, so every walk is bounded.
const (
	metricsDefaultMaxDepth         = 16
	metricsDefaultMaxLeaves        = 2048
	metricsDefaultMaxFieldsPerNode = 256
	metricsDefaultMaxFieldNames    = 4096
	metricsDefaultMaxNameLength    = 128

	metricsCeilMaxDepth         = 32
	metricsCeilMaxLeaves        = 65536
	metricsCeilMaxFieldsPerNode = 4096
	metricsCeilMaxFieldNames    = 65536
	metricsCeilMaxNameLength    = 256

	metricsMaxRules     = 256
	metricsMaxKeyLength = 64 // per JSON key / path segment
	metricsMaxStringLen = 64 // per mapped string value

	metricsWarnInterval = time.Minute
)

// metricsRoots are the respondd sections a rule can start with.
var metricsRoots = map[string]bool{
	"nodeinfo":   true,
	"statistics": true,
	"neighbours": true,
}

// MetricsConfig configures generic numeric fields from respondd data.
type MetricsConfig struct {
	Enable           bool         `toml:"enable"`
	MaxDepth         int          `toml:"max_depth"`
	MaxLeaves        int          `toml:"max_leaves"`
	MaxFieldsPerNode int          `toml:"max_fields_per_node"`
	MaxFieldNames    int          `toml:"max_field_names"`
	MaxNameLength    int          `toml:"max_name_length"`
	Rules            []MetricRule `toml:"rule"`
}

// MetricRule selects respondd values by a static path glob.
type MetricRule struct {
	Path   string             `toml:"path"`
	Values map[string]float64 `toml:"values"`
}

type segmentKind uint8

const (
	segLiteral    segmentKind = iota
	segStar                   // exactly one key
	segDoubleStar             // zero or more keys, last segment only
)

type segment struct {
	kind    segmentKind
	literal string
}

type compiledRule struct {
	segments []segment
	values   map[string]float64
}

type metricsLimits struct {
	maxDepth         int
	maxLeaves        int
	maxFieldsPerNode int
	maxFieldNames    int
	maxNameLength    int
}

type metricsExtractor struct {
	rules  []compiledRule
	limits metricsLimits

	mu       sync.Mutex
	names    map[string]struct{} // distinct field names of all nodes, capped
	lastWarn time.Time
}

func limitOrDefault(name string, value, def, ceil int) (int, error) {
	if value == 0 {
		return def, nil
	}
	if value < 0 || value > ceil {
		return 0, fmt.Errorf("respondd.metrics.%s: %d is out of range 1..%d", name, value, ceil)
	}
	return value, nil
}

// Validate checks the config. It returns nil when metrics are disabled.
func (c *MetricsConfig) Validate() error {
	if !c.Enable {
		return nil
	}
	_, err := newMetricsExtractor(c)
	return err
}

func newMetricsExtractor(c *MetricsConfig) (*metricsExtractor, error) {
	var l metricsLimits
	var err error
	if l.maxDepth, err = limitOrDefault("max_depth", c.MaxDepth, metricsDefaultMaxDepth, metricsCeilMaxDepth); err != nil {
		return nil, err
	}
	if l.maxLeaves, err = limitOrDefault("max_leaves", c.MaxLeaves, metricsDefaultMaxLeaves, metricsCeilMaxLeaves); err != nil {
		return nil, err
	}
	if l.maxFieldsPerNode, err = limitOrDefault("max_fields_per_node", c.MaxFieldsPerNode, metricsDefaultMaxFieldsPerNode, metricsCeilMaxFieldsPerNode); err != nil {
		return nil, err
	}
	if l.maxFieldNames, err = limitOrDefault("max_field_names", c.MaxFieldNames, metricsDefaultMaxFieldNames, metricsCeilMaxFieldNames); err != nil {
		return nil, err
	}
	if l.maxNameLength, err = limitOrDefault("max_name_length", c.MaxNameLength, metricsDefaultMaxNameLength, metricsCeilMaxNameLength); err != nil {
		return nil, err
	}
	if len(c.Rules) > metricsMaxRules {
		return nil, fmt.Errorf("respondd.metrics: too many rules (%d, max %d)", len(c.Rules), metricsMaxRules)
	}

	e := &metricsExtractor{
		limits: l,
		names:  make(map[string]struct{}),
	}
	for i, r := range c.Rules {
		cr, err := compileRule(r, l.maxDepth)
		if err != nil {
			return nil, fmt.Errorf("respondd.metrics.rule[%d] %q: %w", i, r.Path, err)
		}
		e.rules = append(e.rules, cr)
	}
	return e, nil
}

func compileRule(r MetricRule, maxDepth int) (compiledRule, error) {
	parts := strings.Split(r.Path, ".")
	if len(parts) < 2 {
		return compiledRule{}, errors.New("path needs at least two segments")
	}
	if len(parts) > maxDepth {
		return compiledRule{}, fmt.Errorf("path has more than max_depth (%d) segments", maxDepth)
	}
	cr := compiledRule{}
	for i, p := range parts {
		switch p {
		case "*":
			cr.segments = append(cr.segments, segment{kind: segStar})
		case "**":
			if i != len(parts)-1 {
				return compiledRule{}, errors.New(`"**" is only allowed as the last segment`)
			}
			cr.segments = append(cr.segments, segment{kind: segDoubleStar})
		default:
			if !validRuleLiteral(p) {
				return compiledRule{}, fmt.Errorf("invalid segment %q (allowed: a-z 0-9 _ -, length 1..%d, or * / **)", p, metricsMaxKeyLength)
			}
			cr.segments = append(cr.segments, segment{kind: segLiteral, literal: strings.ReplaceAll(p, "-", "_")})
		}
	}
	if cr.segments[0].kind != segLiteral || !metricsRoots[cr.segments[0].literal] {
		return compiledRule{}, errors.New(`first segment must be "nodeinfo", "statistics" or "neighbours"`)
	}
	if len(r.Values) > 0 {
		cr.values = make(map[string]float64, len(r.Values))
		for k, v := range r.Values {
			if len(k) > metricsMaxStringLen {
				return compiledRule{}, fmt.Errorf("value key %q is longer than %d bytes", k, metricsMaxStringLen)
			}
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return compiledRule{}, fmt.Errorf("value for %q is not a finite number", k)
			}
			cr.values[k] = v
		}
	}
	return cr, nil
}

func validRuleLiteral(s string) bool {
	if len(s) == 0 || len(s) > metricsMaxKeyLength {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// sanitizeKey converts a JSON key from node data to a field name segment.
// It accepts only [A-Za-z0-9_-]{1,64}; other keys are rejected (not rewritten).
// The result contains only [a-z0-9_].
func sanitizeKey(s string) (string, bool) {
	if len(s) == 0 || len(s) > metricsMaxKeyLength {
		return "", false
	}
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
			b[i] = c
		case c >= 'A' && c <= 'Z':
			b[i] = c + ('a' - 'A')
		case c == '-':
			b[i] = '_'
		default:
			return "", false
		}
	}
	return string(b), true
}

type ruleState struct {
	rule int
	pos  int
}

type metricsWalk struct {
	e      *metricsExtractor
	out    map[string]float64
	leaves int
	stop   bool
	capHit bool
}

// extract returns the numeric values of jsonData selected by the rules.
// jsonData must already be valid JSON (encoding/json parsed it before).
func (e *metricsExtractor) extract(jsonData []byte) map[string]float64 {
	if e == nil || len(e.rules) == 0 {
		return nil
	}
	root := gjson.ParseBytes(jsonData)
	if !root.IsObject() {
		return nil
	}

	states := make([]ruleState, len(e.rules))
	for i := range e.rules {
		states[i] = ruleState{rule: i}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	w := &metricsWalk{e: e, out: make(map[string]float64)}
	w.walk(root, "", 1, states)

	if w.capHit && time.Since(e.lastWarn) > metricsWarnInterval {
		e.lastWarn = time.Now()
		slog.Warn("respondd metrics: max_field_names reached, new field names are dropped", "max_field_names", e.limits.maxFieldNames)
	}
	if len(w.out) == 0 {
		return nil
	}
	return w.out
}

func (w *metricsWalk) advance(states []ruleState, key string) []ruleState {
	var next []ruleState
	for _, s := range states {
		segs := w.e.rules[s.rule].segments
		if s.pos >= len(segs) {
			continue
		}
		seg := segs[s.pos]
		switch seg.kind {
		case segLiteral:
			if seg.literal == key {
				next = append(next, ruleState{s.rule, s.pos + 1})
			}
		case segStar:
			next = append(next, ruleState{s.rule, s.pos + 1})
		case segDoubleStar:
			next = append(next, s)
		}
	}
	return next
}

func (w *metricsWalk) complete(s ruleState) bool {
	segs := w.e.rules[s.rule].segments
	return s.pos == len(segs) || (s.pos == len(segs)-1 && segs[s.pos].kind == segDoubleStar)
}

func (w *metricsWalk) walk(obj gjson.Result, prefix string, depth int, states []ruleState) {
	if depth > w.e.limits.maxDepth {
		return
	}
	obj.ForEach(func(k, v gjson.Result) bool {
		w.leaves++
		if w.leaves > w.e.limits.maxLeaves {
			w.stop = true
			return false
		}
		if k.Type != gjson.String {
			return true
		}
		key, ok := sanitizeKey(k.Str)
		if !ok {
			return true
		}
		next := w.advance(states, key)
		if len(next) == 0 {
			return true
		}
		name := key
		if prefix != "" {
			name = prefix + "." + key
		}
		if len(name) > w.e.limits.maxNameLength {
			return true
		}
		switch {
		case v.IsObject():
			w.walk(v, name, depth+1, next)
		case v.IsArray():
			// arrays have no stable names; they are not walked
		default:
			w.leaf(name, v, next)
		}
		return !w.stop
	})
}

func (w *metricsWalk) leaf(name string, v gjson.Result, states []ruleState) {
	var value float64
	found := false
	for _, s := range states {
		if !w.complete(s) {
			continue
		}
		switch v.Type {
		case gjson.Number:
			f, err := strconv.ParseFloat(v.Raw, 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return
			}
			value, found = f, true
		case gjson.True:
			value, found = 1, true
		case gjson.False:
			value, found = 0, true
		case gjson.String:
			if values := w.e.rules[s.rule].values; values != nil && len(v.Str) <= metricsMaxStringLen {
				value, found = values[v.Str]
			}
		default:
			return
		}
		if found {
			break
		}
	}
	if found {
		w.emit(name, value)
	}
}

func (w *metricsWalk) emit(name string, value float64) {
	if _, dup := w.out[name]; dup {
		return
	}
	if len(w.out) >= w.e.limits.maxFieldsPerNode {
		w.stop = true
		return
	}
	if _, known := w.e.names[name]; !known {
		if len(w.e.names) >= w.e.limits.maxFieldNames {
			w.capHit = true
			return
		}
		w.e.names[name] = struct{}{}
	}
	w.out[name] = value
}
