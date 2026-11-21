package respond

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"codeberg.org/FreifunkBremen/yanic/data"
)

const parkerStatistics = `{
  "statistics": {
    "node_id": "f81a67a5e9c1",
    "gateway_nexthop": "02:00:00:00:00:01",
    "clat": {
      "v4_to_v6": {"packets": 1200, "bytes": 345678},
      "v6_to_v4": {"packets": 1100, "bytes": 2.5e6}
    },
    "mesh_vpn": {
      "groups": {
        "muc01": {"peers": {"pgw-01": {"established": 3600.5, "method": "active"}}},
        "vie01": {"peers": {"pgw-02": {"established": 12, "method": "standby"}, "pgw-03": null}}
      }
    },
    "uptime": 1234
  }
}`

var parkerRules = []MetricRule{
	{Path: "statistics.clat.{direction}", Measurement: "clat"},
	{
		Path:        "statistics.mesh_vpn.groups.{group}.peers.{peer}",
		Measurement: "mesh_vpn_peer",
		Values:      map[string]map[string]float64{"method": {"active": 1, "standby": 0}},
	},
}

var (
	validFieldNameRe = regexp.MustCompile(`^[a-z0-9_]{1,64}(\.[a-z0-9_]{1,64})*$`)
	validTagRe       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

func mustExtractor(t testing.TB, c MetricsConfig) *metricsExtractor {
	t.Helper()
	c.Enable = true
	e, err := newMetricsExtractor(&c)
	require.NoError(t, err)
	return e
}

func sortPoints(points []data.MetricPoint) []data.MetricPoint {
	sort.SliceStable(points, func(i, j int) bool {
		return fmt.Sprint(points[i].Measurement, points[i].Tags) < fmt.Sprint(points[j].Measurement, points[j].Tags)
	})
	return points
}

func TestMetricsParker(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: parkerRules})
	got := sortPoints(e.extract([]byte(parkerStatistics)))
	assert.Equal(t, []data.MetricPoint{
		{Section: "statistics", Measurement: "clat", Tags: map[string]string{"direction": "v4_to_v6"}, Fields: map[string]float64{"packets": 1200, "bytes": 345678}},
		{Section: "statistics", Measurement: "clat", Tags: map[string]string{"direction": "v6_to_v4"}, Fields: map[string]float64{"packets": 1100, "bytes": 2.5e6}},
		{Section: "statistics", Measurement: "mesh_vpn_peer", Tags: map[string]string{"group": "muc01", "peer": "pgw-01"}, Fields: map[string]float64{"established": 3600.5, "method": 1}},
		{Section: "statistics", Measurement: "mesh_vpn_peer", Tags: map[string]string{"group": "vie01", "peer": "pgw-02"}, Fields: map[string]float64{"established": 12, "method": 0}},
	}, got)

	// field names are never keys matched by a capture
	for _, p := range got {
		for name := range p.Fields {
			for _, tag := range p.Tags {
				assert.NotContains(t, name, strings.ToLower(tag))
			}
		}
	}
}

func TestMetricsFieldList(t *testing.T) {
	// one point with nested literal fields
	e := mustExtractor(t, MetricsConfig{Rules: []MetricRule{{
		Path: "statistics.clat", Measurement: "clat_total",
		Fields: []string{"v4_to_v6.packets", "v6-to-v4.bytes", "missing", "v4_to_v6"},
	}}})
	got := e.extract([]byte(parkerStatistics))
	require.Len(t, got, 1)
	assert.Empty(t, got[0].Tags)
	assert.Equal(t, map[string]float64{"v4_to_v6.packets": 1200, "v6_to_v4.bytes": 2.5e6}, got[0].Fields)
}

func TestMetricsDirectLeavesOnly(t *testing.T) {
	// without a field list, nested objects with dynamic keys are never flattened
	e := mustExtractor(t, MetricsConfig{Rules: []MetricRule{{Path: "statistics.mesh_vpn.groups.{group}", Measurement: "g"}}})
	got := e.extract([]byte(`{"statistics":{"mesh_vpn":{"groups":{"muc01":{"peers":{"pgw01":{"established":1}},"count":2}}}}}`))
	require.Len(t, got, 1)
	assert.Equal(t, map[string]float64{"count": 2}, got[0].Fields)
}

func TestMetricsDisabledOrNoRules(t *testing.T) {
	var nilExtractor *metricsExtractor
	assert.Nil(t, nilExtractor.extract([]byte(parkerStatistics)))
	assert.Nil(t, mustExtractor(t, MetricsConfig{}).extract([]byte(parkerStatistics)))
}

func TestMetricsValueTypes(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: []MetricRule{{
		Path: "statistics.x", Measurement: "x",
		Values: map[string]map[string]float64{"m": {"up": 1}, "n": {"up": 1}},
	}}})
	got := e.extract([]byte(`{"statistics":{"x":{"t":true,"f":false,"s":"text","m":"down","n":"up","z":null,"a":[1,2],"o":{"v":1}}}}`))
	require.Len(t, got, 1)
	assert.Equal(t, map[string]float64{"t": 1, "f": 0, "n": 1}, got[0].Fields)

	// a point without fields is not written
	assert.Empty(t, e.extract([]byte(`{"statistics":{"x":{"s":"text"}}}`)))
}

func TestMetricsConfigValidation(t *testing.T) {
	badPaths := []string{
		"", "other.x", "{x}.y", "statistics.A", "statistics.a b", "statistics.a\nb",
		"statistics.a,b=1", "statistics..x", "statistics.*", "statistics.**", "statistics.[a-z]",
		"statistics.a|@reverse", "statistics.{}", "statistics.{X}", "statistics.{1a}",
		"statistics.{a b}", "statistics.{nodeid}", "statistics.{hostname}", "statistics.{a}.{a}",
		"statistics.{{a}}", "statistics." + strings.Repeat("a", 65), "statistics." + strings.Repeat("a.", 20) + "a",
	}
	for _, p := range badPaths {
		c := MetricsConfig{Enable: true, Rules: []MetricRule{{Path: p, Measurement: "m"}}}
		assert.Error(t, c.Validate(), "path %q", p)
	}

	for _, m := range []string{"", "node", "link", "dhcp", "global", "firmware", "model", "autoupdater",
		"A", "1m", "m-x", "m x", "m\n", "m,x=1", `m"`, strings.Repeat("m", 65)} {
		c := MetricsConfig{Enable: true, Rules: []MetricRule{{Path: "statistics.x", Measurement: m}}}
		assert.Error(t, c.Validate(), "measurement %q", m)
	}

	for _, f := range []string{"", "a b", "a..b", ".a", "a.", "*", "{a}", "a=b", strings.Repeat("a", 129)} {
		c := MetricsConfig{Enable: true, Rules: []MetricRule{{Path: "statistics.x", Measurement: "m", Fields: []string{f}}}}
		assert.Error(t, c.Validate(), "field %q", f)
	}

	for _, c := range []MetricsConfig{
		{MaxDepth: -1}, {MaxDepth: 33}, {MaxLeaves: 1 << 20}, {MaxPointsPerNode: 5000},
		{MaxFieldsPerPoint: 5000}, {MaxTagValues: 1 << 20}, {MaxFieldNames: 1 << 20}, {MaxNameLength: 1000},
		{Rules: make([]MetricRule, metricsMaxRules+1)},
		{Rules: []MetricRule{{Path: "statistics.x", Measurement: "m", Values: map[string]map[string]float64{"a": {"x": math.Inf(1)}}}}},
		{Rules: []MetricRule{{Path: "statistics.x", Measurement: "m", Values: map[string]map[string]float64{"a": {strings.Repeat("a", 65): 1}}}}},
		{Rules: []MetricRule{{Path: "statistics.x", Measurement: "m", Values: map[string]map[string]float64{"a b": {"x": 1}}}}},
	} {
		c.Enable = true
		assert.Error(t, c.Validate(), "%+v", c)
	}

	// disabled config is never validated
	assert.NoError(t, (&MetricsConfig{Rules: []MetricRule{{Path: "bad"}}}).Validate())
	assert.NoError(t, (&MetricsConfig{Enable: true, Rules: parkerRules}).Validate())
}

func TestMetricsConfigTOML(t *testing.T) {
	var c Config
	_, err := toml.Decode(`
[metrics]
enable = true
max_points_per_node = 32
[[metrics.rule]]
path = "statistics.clat.{direction}"
measurement = "clat"
[[metrics.rule]]
path = "statistics.mesh_vpn.groups.{group}.peers.{peer}"
measurement = "mesh_vpn_peer"
fields = ["established", "method"]
values = { method = { active = 1, standby = 0 } }
`, &c)
	require.NoError(t, err)
	require.NoError(t, c.Metrics.Validate())
	assert.Equal(t, 32, c.Metrics.MaxPointsPerNode)
	assert.Equal(t, map[string]map[string]float64{"method": {"active": 1, "standby": 0}}, c.Metrics.Rules[1].Values)
}

func TestMetricsMaliciousKeys(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: []MetricRule{{Path: "statistics.peers.{peer}", Measurement: "p"}}})
	keys := []string{
		"a b", "a\nb", "a,b", "a=b", `a"b`, "a\\b", "a.b", "ä", "a\u0000b", "a\u2028b",
		"x value=1i 1", "x\nnode,nodeid=evil value=1", "*", "", strings.Repeat("k", 65),
	}
	peers := map[string]any{}
	for _, k := range keys {
		peers[k] = map[string]any{"v": 1, k: 2}
	}
	peers["ok-Peer"] = map[string]any{"v": 1}
	for _, k := range keys {
		peers["ok-Peer"].(map[string]any)[k] = 3
	}
	payload, err := json.Marshal(map[string]any{"statistics": map[string]any{"peers": peers}})
	require.NoError(t, err)

	got := e.extract(payload)
	require.Len(t, got, 1)
	assert.Equal(t, map[string]string{"peer": "ok-Peer"}, got[0].Tags)
	assert.Equal(t, map[string]float64{"v": 1}, got[0].Fields)
}

func TestMetricsNumbers(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: []MetricRule{{Path: "statistics.n", Measurement: "n"}}})
	got := e.extract([]byte(`{"statistics":{"n":{"big":1e999,"neg":-1e999,"int":18446744073709551615,"small":1e-400,"f":-0.5,"i":42}}}`))
	require.Len(t, got, 1)
	assert.Equal(t, map[string]float64{"int": 18446744073709551615, "small": 0, "f": -0.5, "i": 42}, got[0].Fields)
}

func TestMetricsCollisionAndDuplicates(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: []MetricRule{{Path: "statistics.p.{peer}", Measurement: "p"}}})
	got := e.extract([]byte(`{"statistics":{"p":{"a":{"A-b":1,"a_b":2,"a-B":3},"a":{"x":1}}}}`))
	require.Len(t, got, 1)
	assert.Equal(t, map[string]float64{"a_b": 1}, got[0].Fields)
}

func TestMetricsDeepNesting(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: []MetricRule{{Path: "statistics.{a}.{b}.{c}", Measurement: "d"}}})
	deep := `{"statistics":` + strings.Repeat(`{"a":`, 5000) + `{"v":1}` + strings.Repeat("}", 5000) + "}"
	assert.Empty(t, e.extract([]byte(deep)))

	ok := `{"statistics":{"a":{"b":{"c":{"v":1}}}}}`
	got := e.extract([]byte(ok))
	require.Len(t, got, 1)
	assert.Equal(t, map[string]string{"a": "a", "b": "b", "c": "c"}, got[0].Tags)
}

func hugePeers(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"statistics":{"mesh_vpn":{"groups":{"g":{"peers":{`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"p%d":{"established":%d}`, i, i)
	}
	b.WriteString(`}}}}}}`)
	return []byte(b.String())
}

func TestMetricsHugeMap(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: parkerRules})
	got := e.extract(hugePeers(100000))
	assert.NotEmpty(t, got)
	assert.LessOrEqual(t, len(got), metricsDefaultMaxPointsPerNode)
}

func TestMetricsLeafLimit(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{MaxLeaves: 20, Rules: parkerRules})
	got := e.extract(hugePeers(100))
	assert.Less(t, len(got), 10)
}

func TestMetricsFieldsPerPoint(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{MaxFieldsPerPoint: 3, Rules: []MetricRule{{Path: "statistics.x", Measurement: "x"}}})
	got := e.extract([]byte(`{"statistics":{"x":{"a":1,"b":2,"c":3,"d":4,"e":5}}}`))
	require.Len(t, got, 1)
	assert.Len(t, got[0].Fields, 3)
}

func TestMetricsNameLength(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{MaxNameLength: 10, Rules: []MetricRule{{Path: "statistics.x", Measurement: "x"}}})
	got := e.extract([]byte(`{"statistics":{"x":{"short":1,"averyverylongname":2}}}`))
	require.Len(t, got, 1)
	assert.Equal(t, map[string]float64{"short": 1}, got[0].Fields)
}

func TestMetricsTagValueCap(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{MaxTagValues: 5, Rules: parkerRules})
	peers := map[string]bool{}
	for n := 0; n < 100; n++ {
		in := fmt.Sprintf(`{"statistics":{"mesh_vpn":{"groups":{"g":{"peers":{"node%d":{"established":1},"shared":{"established":2}}}}}}}`, n)
		for _, p := range e.extract([]byte(in)) {
			peers[p.Tags["peer"]] = true
		}
	}
	assert.Len(t, peers, 5)
	assert.Len(t, e.tagValues["mesh_vpn_peer\x00peer"], 5)
	// known values still work after the cap
	got := e.extract([]byte(`{"statistics":{"mesh_vpn":{"groups":{"g":{"peers":{"shared":{"established":3},"new":{"established":1}}}}}}}`))
	require.Len(t, got, 1)
	assert.Equal(t, "shared", got[0].Tags["peer"])
}

func TestMetricsFieldNameCap(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{MaxFieldNames: 3, Rules: []MetricRule{{Path: "statistics.x", Measurement: "x"}}})
	for n := 0; n < 50; n++ {
		e.extract([]byte(fmt.Sprintf(`{"statistics":{"x":{"a":1,"f%d":1}}}`, n)))
	}
	assert.Len(t, e.fieldNames["x"], 3)
}

func TestMetricsRootMustBeObject(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: parkerRules})
	for _, in := range []string{`[]`, `1`, `"x"`, `null`, `{"statistics":[1,2]}`, `{"statistics":"x"}`,
		`{"statistics":{"clat":{"v4":1}}}`, `{"statistics":{"clat":[{"v":1}]}}`} {
		assert.Empty(t, e.extract([]byte(in)), in)
	}
}

func deflate(t testing.TB, b []byte) []byte {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	require.NoError(t, err)
	_, err = w.Write(b)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func TestParseMetrics(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: parkerRules})
	res := &Response{Raw: deflate(t, []byte(parkerStatistics))}
	rdata, err := res.parse(nil, e)
	require.NoError(t, err)
	assert.Equal(t, "f81a67a5e9c1", rdata.Statistics.NodeID)
	assert.Len(t, rdata.Metrics, 4)

	// without extractor there are no metrics
	rdata, err = res.parse(nil, nil)
	require.NoError(t, err)
	assert.Nil(t, rdata.Metrics)
}

func TestParseDecompressionLimit(t *testing.T) {
	bomb := []byte(`{"statistics":{"node_id":"` + strings.Repeat("a", MaxDecompressedSize) + `"}}`)
	raw := deflate(t, bomb)
	assert.Less(t, len(raw), MaxDataGramSize)
	_, err := (&Response{Raw: raw}).parse(nil, nil)
	assert.Error(t, err)
}

func TestDropOrphanMetrics(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: []MetricRule{{Path: "statistics.x", Measurement: "s"}, {Path: "nodeinfo.x", Measurement: "n"}}})
	rdata, err := (&Response{Raw: deflate(t, []byte(`{"statistics":{"node_id":"aaaaaaaaaaaa","x":{"v":1}},"nodeinfo":{"node_id":"bbbbbbbbbbbb","x":{"v":2}}}`))}).parse(nil, e)
	require.NoError(t, err)
	require.Len(t, rdata.Metrics, 2)
	rdata.Nodeinfo = nil
	dropOrphanMetrics(rdata)
	require.Len(t, rdata.Metrics, 1)
	assert.Equal(t, "s", rdata.Metrics[0].Measurement)
}

func FuzzExtractMetrics(f *testing.F) {
	f.Add([]byte(parkerStatistics))
	f.Add([]byte(`{"statistics":{"a b":1,"x\n":2,"clat":{"v":{"x":1e999}}}}`))
	f.Add([]byte(`{"statistics":{"mesh_vpn":{"groups":{"g":{"peers":{"p":{"method":"active"}}}}}}}`))
	f.Add([]byte(`{"statistics":` + strings.Repeat(`{"a":`, 40) + "1" + strings.Repeat("}", 40) + "}"))

	rules := append([]MetricRule{
		{Path: "statistics.{a}", Measurement: "a"},
		{Path: "statistics.{a}.{b}", Measurement: "b"},
		{Path: "nodeinfo.x", Measurement: "c", Fields: []string{"a.b", "c"}},
	}, parkerRules...)
	f.Fuzz(func(t *testing.T, in []byte) {
		e := mustExtractor(t, MetricsConfig{MaxPointsPerNode: 16, MaxFieldsPerPoint: 16, Rules: rules})
		if !json.Valid(in) {
			return
		}
		got := e.extract(in)
		if len(got) > 16 {
			t.Fatalf("too many points: %d", len(got))
		}
		for _, p := range got {
			if !data.ValidMetricsMeasurement(p.Measurement) {
				t.Fatalf("invalid measurement %q", p.Measurement)
			}
			if len(p.Fields) == 0 || len(p.Fields) > 16 {
				t.Fatalf("bad field count %d", len(p.Fields))
			}
			for k, v := range p.Tags {
				if !data.ValidMetricsIdentifier(k, 32) || data.MetricsReservedTags[k] || !validTagRe.MatchString(v) {
					t.Fatalf("invalid tag %q=%q", k, v)
				}
			}
			for k, v := range p.Fields {
				if !validFieldNameRe.MatchString(k) || len(k) > metricsDefaultMaxNameLength {
					t.Fatalf("invalid field name %q", k)
				}
				if math.IsNaN(v) || math.IsInf(v, 0) {
					t.Fatalf("non-finite value for %q", k)
				}
			}
		}

		// the full parse path must not panic either
		_, _ = (&Response{Raw: deflate(t, in)}).parse(nil, e)
		_, _ = (&Response{Raw: in}).parse(nil, e)
	})
}
