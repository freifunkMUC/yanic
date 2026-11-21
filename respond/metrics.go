package respond

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"

	"codeberg.org/FreifunkBremen/yanic/data"
)

// Defaults and hard ceilings for [respondd.metrics]. Respondd data is
// untrusted input from any node in the mesh, so every walk is bounded.
const (
	metricsDefaultMaxDepth          = 16
	metricsDefaultMaxLeaves         = 2048
	metricsDefaultMaxPointsPerNode  = 64
	metricsDefaultMaxFieldsPerPoint = 64
	metricsDefaultMaxTagValues      = 1024
	metricsDefaultMaxFieldNames     = 256
	metricsDefaultMaxNameLength     = 128

	metricsCeilMaxDepth          = 32
	metricsCeilMaxLeaves         = 65536
	metricsCeilMaxPointsPerNode  = 1024
	metricsCeilMaxFieldsPerPoint = 1024
	metricsCeilMaxTagValues      = 65536
	metricsCeilMaxFieldNames     = 4096
	metricsCeilMaxNameLength     = 256

	metricsMaxRules     = 256
	metricsMaxKeyLength = 64 // per JSON key, path segment and tag value
	metricsMaxStringLen = 64 // per mapped string value

	metricsWarnInterval = time.Minute
)

// metricsRoots are the respondd sections a rule can start with.
var metricsRoots = map[string]bool{
	"nodeinfo":   true,
	"statistics": true,
	"neighbours": true,
}

// MetricsConfig configures generic metrics from respondd data.
type MetricsConfig struct {
	Enable            bool         `toml:"enable"`
	MaxDepth          int          `toml:"max_depth"`
	MaxLeaves         int          `toml:"max_leaves"`
	MaxPointsPerNode  int          `toml:"max_points_per_node"`
	MaxFieldsPerPoint int          `toml:"max_fields_per_point"`
	MaxTagValues      int          `toml:"max_tag_values"`
	MaxFieldNames     int          `toml:"max_field_names"`
	MaxNameLength     int          `toml:"max_name_length"`
	Rules             []MetricRule `toml:"rule"`
}

// MetricRule selects respondd objects by a static path. Each matched
// object becomes one point in Measurement.
type MetricRule struct {
	Path        string                        `toml:"path"`
	Measurement string                        `toml:"measurement"`
	Fields      []string                      `toml:"fields"`
	Values      map[string]map[string]float64 `toml:"values"`
}

type segment struct {
	capture string // tag name, if the segment is {name}
	literal string
}

type compiledRule struct {
	measurement string
	section     string
	segments    []segment
	fields      [][]string // nil: all direct leaves
	fieldNames  []string
	values      map[string]map[string]float64
}

type metricsLimits struct {
	maxDepth          int
	maxLeaves         int
	maxPointsPerNode  int
	maxFieldsPerPoint int
	maxTagValues      int
	maxFieldNames     int
	maxNameLength     int
}

type metricsExtractor struct {
	rules  []compiledRule
	limits metricsLimits

	mu         sync.Mutex
	tagValues  map[string]map[string]struct{} // "measurement\x00tag" -> values, capped
	fieldNames map[string]map[string]struct{} // measurement -> field names, capped
	lastWarn   time.Time
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
	for _, x := range []struct {
		name            string
		dst             *int
		value, def, max int
	}{
		{"max_depth", &l.maxDepth, c.MaxDepth, metricsDefaultMaxDepth, metricsCeilMaxDepth},
		{"max_leaves", &l.maxLeaves, c.MaxLeaves, metricsDefaultMaxLeaves, metricsCeilMaxLeaves},
		{"max_points_per_node", &l.maxPointsPerNode, c.MaxPointsPerNode, metricsDefaultMaxPointsPerNode, metricsCeilMaxPointsPerNode},
		{"max_fields_per_point", &l.maxFieldsPerPoint, c.MaxFieldsPerPoint, metricsDefaultMaxFieldsPerPoint, metricsCeilMaxFieldsPerPoint},
		{"max_tag_values", &l.maxTagValues, c.MaxTagValues, metricsDefaultMaxTagValues, metricsCeilMaxTagValues},
		{"max_field_names", &l.maxFieldNames, c.MaxFieldNames, metricsDefaultMaxFieldNames, metricsCeilMaxFieldNames},
		{"max_name_length", &l.maxNameLength, c.MaxNameLength, metricsDefaultMaxNameLength, metricsCeilMaxNameLength},
	} {
		v, err := limitOrDefault(x.name, x.value, x.def, x.max)
		if err != nil {
			return nil, err
		}
		*x.dst = v
	}
	if len(c.Rules) > metricsMaxRules {
		return nil, fmt.Errorf("respondd.metrics: too many rules (%d, max %d)", len(c.Rules), metricsMaxRules)
	}

	e := &metricsExtractor{
		limits:     l,
		tagValues:  make(map[string]map[string]struct{}),
		fieldNames: make(map[string]map[string]struct{}),
	}
	for i, r := range c.Rules {
		cr, err := compileRule(r, l)
		if err != nil {
			return nil, fmt.Errorf("respondd.metrics.rule[%d] %q: %w", i, r.Path, err)
		}
		e.rules = append(e.rules, cr)
	}
	return e, nil
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

// parseFieldPath validates a relative field path from the config
// and returns its normalized segments.
func parseFieldPath(p string, maxLen int) ([]string, error) {
	if len(p) == 0 || len(p) > maxLen {
		return nil, fmt.Errorf("field %q: length must be 1..%d", p, maxLen)
	}
	parts := strings.Split(p, ".")
	for i, s := range parts {
		if !validRuleLiteral(s) {
			return nil, fmt.Errorf("field %q: invalid segment %q (allowed: a-z 0-9 _ -)", p, s)
		}
		parts[i] = strings.ReplaceAll(s, "-", "_")
	}
	return parts, nil
}

func compileRule(r MetricRule, l metricsLimits) (compiledRule, error) {
	cr := compiledRule{measurement: r.Measurement}
	if !data.ValidMetricsMeasurement(r.Measurement) {
		return cr, fmt.Errorf("invalid measurement %q (allowed: [a-z][a-z0-9_]{0,63}, not a built-in measurement)", r.Measurement)
	}

	parts := strings.Split(r.Path, ".")
	if len(parts) > l.maxDepth {
		return cr, fmt.Errorf("path has more than max_depth (%d) segments", l.maxDepth)
	}
	captures := map[string]bool{}
	for _, p := range parts {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			name := p[1 : len(p)-1]
			if !data.ValidMetricsIdentifier(name, 32) {
				return cr, fmt.Errorf("invalid capture %q (allowed: [a-z][a-z0-9_]{0,31})", p)
			}
			if data.MetricsReservedTags[name] || captures[name] {
				return cr, fmt.Errorf("capture name %q is reserved or used twice", name)
			}
			captures[name] = true
			cr.segments = append(cr.segments, segment{capture: name})
			continue
		}
		if !validRuleLiteral(p) {
			return cr, fmt.Errorf("invalid segment %q (allowed: a-z 0-9 _ -, length 1..%d, or {name})", p, metricsMaxKeyLength)
		}
		cr.segments = append(cr.segments, segment{literal: strings.ReplaceAll(p, "-", "_")})
	}
	if cr.segments[0].capture != "" || !metricsRoots[cr.segments[0].literal] {
		return cr, errors.New(`first segment must be "nodeinfo", "statistics" or "neighbours"`)
	}
	cr.section = cr.segments[0].literal

	for _, f := range r.Fields {
		parts, err := parseFieldPath(f, l.maxNameLength)
		if err != nil {
			return cr, err
		}
		if len(cr.segments)+len(parts) > l.maxDepth {
			return cr, fmt.Errorf("field %q: path is deeper than max_depth (%d)", f, l.maxDepth)
		}
		cr.fields = append(cr.fields, parts)
		cr.fieldNames = append(cr.fieldNames, strings.Join(parts, "."))
	}
	if len(cr.fields) > l.maxFieldsPerPoint {
		return cr, fmt.Errorf("more than max_fields_per_point (%d) fields", l.maxFieldsPerPoint)
	}

	if len(r.Values) > 0 {
		cr.values = make(map[string]map[string]float64, len(r.Values))
		for field, m := range r.Values {
			parts, err := parseFieldPath(field, l.maxNameLength)
			if err != nil {
				return cr, fmt.Errorf("values: %w", err)
			}
			name := strings.Join(parts, ".")
			cr.values[name] = make(map[string]float64, len(m))
			for k, v := range m {
				if len(k) > metricsMaxStringLen {
					return cr, fmt.Errorf("values.%s: key %q is longer than %d bytes", field, k, metricsMaxStringLen)
				}
				if math.IsNaN(v) || math.IsInf(v, 0) {
					return cr, fmt.Errorf("values.%s: value for %q is not a finite number", field, k)
				}
				cr.values[name][k] = v
			}
		}
	}
	return cr, nil
}

// sanitizeKey converts a JSON key from node data for literal comparison
// and field names. It accepts only [A-Za-z0-9_-]{1,64}; other keys are
// rejected (not rewritten). The result contains only [a-z0-9_].
func sanitizeKey(s string) (string, bool) {
	if !validTagValue(s) {
		return "", false
	}
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		} else if c == '-' {
			b[i] = '_'
		}
	}
	return string(b), true
}

// validTagValue checks [A-Za-z0-9_-]{1,64}. Captured keys are used
// unchanged as tag values.
func validTagValue(s string) bool {
	if len(s) == 0 || len(s) > metricsMaxKeyLength {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

type metricsWalk struct {
	e      *metricsExtractor
	points []data.MetricPoint
	seen   map[string]bool // measurement + tag set, against duplicate points
	leaves int
	stop   bool
	capHit bool
}

func (w *metricsWalk) visit() bool {
	w.leaves++
	if w.leaves > w.e.limits.maxLeaves {
		w.stop = true
	}
	return !w.stop
}

// child returns the value of the first key of obj that matches literal.
func (w *metricsWalk) child(obj gjson.Result, literal string) (gjson.Result, bool) {
	var res gjson.Result
	found := false
	obj.ForEach(func(k, v gjson.Result) bool {
		if !w.visit() {
			return false
		}
		if key, ok := sanitizeKey(k.Str); ok && k.Type == gjson.String && key == literal {
			res, found = v, true
			return false
		}
		return true
	})
	return res, found
}

// extract returns the points selected by the rules.
// jsonData must already be valid JSON (encoding/json parsed it before).
func (e *metricsExtractor) extract(jsonData []byte) []data.MetricPoint {
	if e == nil || len(e.rules) == 0 {
		return nil
	}
	root := gjson.ParseBytes(jsonData)
	if !root.IsObject() {
		return nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	w := &metricsWalk{e: e, seen: map[string]bool{}}
	for i := range e.rules {
		if w.stop {
			break
		}
		w.match(&e.rules[i], root, 0, nil)
	}

	if w.capHit && time.Since(e.lastWarn) > metricsWarnInterval {
		e.lastWarn = time.Now()
		slog.Warn("respondd metrics: max_tag_values or max_field_names reached, new values are dropped",
			"max_tag_values", e.limits.maxTagValues, "max_field_names", e.limits.maxFieldNames)
	}
	return w.points
}

func (w *metricsWalk) match(r *compiledRule, obj gjson.Result, pos int, captures []string) {
	if w.stop || !obj.IsObject() {
		return
	}
	if pos == len(r.segments) {
		w.point(r, obj, captures)
		return
	}
	seg := r.segments[pos]
	if seg.capture == "" {
		if v, ok := w.child(obj, seg.literal); ok {
			w.match(r, v, pos+1, captures)
		}
		return
	}
	obj.ForEach(func(k, v gjson.Result) bool {
		if !w.visit() {
			return false
		}
		if k.Type == gjson.String && validTagValue(k.Str) && v.IsObject() {
			w.match(r, v, pos+1, append(captures[:len(captures):len(captures)], k.Str))
		}
		return !w.stop
	})
}

func (w *metricsWalk) point(r *compiledRule, obj gjson.Result, captures []string) {
	if len(w.points) >= w.e.limits.maxPointsPerNode {
		w.stop = true
		return
	}

	tags := make(map[string]string, len(captures))
	id := r.measurement
	ci := 0
	for _, seg := range r.segments {
		if seg.capture != "" {
			tags[seg.capture] = captures[ci]
			id += "\x00" + captures[ci]
			ci++
		}
	}
	if w.seen[id] {
		return
	}

	fields := map[string]float64{}
	if r.fields == nil {
		obj.ForEach(func(k, v gjson.Result) bool {
			if !w.visit() {
				return false
			}
			name, ok := sanitizeKey(k.Str)
			if !ok || k.Type != gjson.String || len(name) > w.e.limits.maxNameLength {
				return true
			}
			w.addField(r, fields, name, v)
			return len(fields) < w.e.limits.maxFieldsPerPoint
		})
	} else {
		for i, path := range r.fields {
			v, ok := obj, true
			for _, seg := range path {
				if v, ok = w.child(v, seg); !ok {
					break
				}
			}
			if ok {
				w.addField(r, fields, r.fieldNames[i], v)
			}
		}
	}
	if len(fields) == 0 || w.stop {
		return
	}

	// Register tag values last: a point without fields uses no tag slot.
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !register(w.e.tagValues, r.measurement+"\x00"+k, tags[k], w.e.limits.maxTagValues) {
			w.capHit = true
			return
		}
	}

	w.seen[id] = true
	w.points = append(w.points, data.MetricPoint{
		Section:     r.section,
		Measurement: r.measurement,
		Tags:        tags,
		Fields:      fields,
	})
}

func (w *metricsWalk) addField(r *compiledRule, fields map[string]float64, name string, v gjson.Result) {
	if _, dup := fields[name]; dup {
		return
	}
	var value float64
	switch v.Type {
	case gjson.Number:
		f, err := strconv.ParseFloat(v.Raw, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return
		}
		value = f
	case gjson.True:
		value = 1
	case gjson.False:
		value = 0
	case gjson.String:
		m := r.values[name]
		if m == nil || len(v.Str) > metricsMaxStringLen {
			return
		}
		var ok bool
		if value, ok = m[v.Str]; !ok {
			return
		}
	default:
		return
	}
	if !register(w.e.fieldNames, r.measurement, name, w.e.limits.maxFieldNames) {
		w.capHit = true
		return
	}
	fields[name] = value
}

// register adds value to set[key] if it is known or the set has room.
func register(sets map[string]map[string]struct{}, key, value string, max int) bool {
	set := sets[key]
	if set == nil {
		set = make(map[string]struct{})
		sets[key] = set
	}
	if _, ok := set[value]; ok {
		return true
	}
	if len(set) >= max {
		return false
	}
	set[value] = struct{}{}
	return true
}
