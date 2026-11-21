package respond

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	{Path: "statistics.clat.**"},
	{Path: "statistics.mesh_vpn.groups.*.peers.*.established"},
	{Path: "statistics.mesh_vpn.groups.*.peers.*.method", Values: map[string]float64{"active": 1, "standby": 0}},
}

var validMetricNameRe = regexp.MustCompile(`^(statistics|nodeinfo|neighbours)(\.[a-z0-9_]{1,64})+$`)

func mustExtractor(t testing.TB, c MetricsConfig) *metricsExtractor {
	t.Helper()
	c.Enable = true
	e, err := newMetricsExtractor(&c)
	require.NoError(t, err)
	return e
}

func TestMetricsParker(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: parkerRules})
	got := e.extract([]byte(parkerStatistics))
	assert.Equal(t, map[string]float64{
		"statistics.clat.v4_to_v6.packets":                          1200,
		"statistics.clat.v4_to_v6.bytes":                            345678,
		"statistics.clat.v6_to_v4.packets":                          1100,
		"statistics.clat.v6_to_v4.bytes":                            2.5e6,
		"statistics.mesh_vpn.groups.muc01.peers.pgw_01.established": 3600.5,
		"statistics.mesh_vpn.groups.muc01.peers.pgw_01.method":      1,
		"statistics.mesh_vpn.groups.vie01.peers.pgw_02.established": 12,
		"statistics.mesh_vpn.groups.vie01.peers.pgw_02.method":      0,
	}, got)
}

func TestMetricsDisabledOrNoRules(t *testing.T) {
	var nilExtractor *metricsExtractor
	assert.Nil(t, nilExtractor.extract([]byte(parkerStatistics)))
	assert.Nil(t, mustExtractor(t, MetricsConfig{}).extract([]byte(parkerStatistics)))
}

func TestMetricsBoolAndStrings(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: []MetricRule{
		{Path: "statistics.x.*"},
		{Path: "statistics.m", Values: map[string]float64{"up": 1}},
	}})
	got := e.extract([]byte(`{"statistics":{"x":{"t":true,"f":false,"s":"text","n":null,"a":[1,2]},"m":"down"}}`))
	assert.Equal(t, map[string]float64{"statistics.x.t": 1, "statistics.x.f": 0}, got)
}

func TestMetricsConfigValidation(t *testing.T) {
	bad := []string{
		"",
		"statistics",
		"statistics.**.x",
		"statistics.a.**.**",
		"other.x",
		"*.x",
		"statistics.A",
		"statistics.a b",
		"statistics.a\nb",
		"statistics.a,b=1",
		"statistics..x",
		"statistics.[a-z]",
		"statistics.a|@reverse",
		"statistics." + strings.Repeat("a", 65),
		"statistics." + strings.Repeat("a.", 20) + "a",
	}
	for _, p := range bad {
		c := MetricsConfig{Enable: true, Rules: []MetricRule{{Path: p}}}
		assert.Error(t, c.Validate(), "path %q", p)
	}

	limits := []MetricsConfig{
		{MaxDepth: -1}, {MaxDepth: 33}, {MaxLeaves: 1 << 20}, {MaxFieldsPerNode: 5000},
		{MaxFieldNames: 1 << 20}, {MaxNameLength: 1000},
	}
	for _, c := range limits {
		c.Enable = true
		assert.Error(t, c.Validate(), "%+v", c)
	}

	c := MetricsConfig{Enable: true, Rules: []MetricRule{{Path: "statistics.x", Values: map[string]float64{"a": math.Inf(1)}}}}
	assert.Error(t, c.Validate())
	c = MetricsConfig{Enable: true, Rules: []MetricRule{{Path: "statistics.x", Values: map[string]float64{strings.Repeat("a", 65): 1}}}}
	assert.Error(t, c.Validate())

	c = MetricsConfig{Enable: true, Rules: make([]MetricRule, metricsMaxRules+1)}
	assert.Error(t, c.Validate())

	// disabled config is never validated
	assert.NoError(t, (&MetricsConfig{Rules: []MetricRule{{Path: "bad"}}}).Validate())
	assert.NoError(t, (&MetricsConfig{Enable: true, Rules: parkerRules}).Validate())
}

func TestMetricsConfigTOML(t *testing.T) {
	var c Config
	_, err := toml.Decode(`
[metrics]
enable = true
max_fields_per_node = 64
[[metrics.rule]]
path = "statistics.clat.**"
[[metrics.rule]]
path = "statistics.mesh_vpn.groups.*.peers.*.method"
values = { active = 1, standby = 0.5 }
`, &c)
	require.NoError(t, err)
	require.NoError(t, c.Metrics.Validate())
	assert.Equal(t, 64, c.Metrics.MaxFieldsPerNode)
	assert.Equal(t, map[string]float64{"active": 1, "standby": 0.5}, c.Metrics.Rules[1].Values)
}

func TestMetricsMaliciousKeys(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: []MetricRule{{Path: "statistics.**"}}})
	keys := []string{
		"a b", "a\nb", "a,b", "a=b", `a"b`, "a\\b", "a.b", "ä", "a\u0000b", "a\u2028b",
		"x value=1i 1", "x\nnode,nodeid=evil value=1", "*", "", strings.Repeat("k", 65),
	}
	obj := map[string]any{}
	for _, k := range keys {
		obj[k] = 1
	}
	obj["ok-Key"] = 2
	payload, err := json.Marshal(map[string]any{"statistics": obj})
	require.NoError(t, err)

	got := e.extract(payload)
	assert.Equal(t, map[string]float64{"statistics.ok_key": 2}, got)
}

func TestMetricsNumbers(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: []MetricRule{{Path: "statistics.**"}}})
	got := e.extract([]byte(`{"statistics":{"big":1e999,"neg":-1e999,"int":18446744073709551615,"small":1e-400,"f":-0.5,"i":42}}`))
	assert.Equal(t, map[string]float64{
		"statistics.int":   18446744073709551615,
		"statistics.small": 0,
		"statistics.f":     -0.5,
		"statistics.i":     42,
	}, got)
	for _, v := range got {
		assert.False(t, math.IsNaN(v) || math.IsInf(v, 0))
	}
}

func TestMetricsCollisionAndDuplicates(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: []MetricRule{{Path: "statistics.*"}}})
	got := e.extract([]byte(`{"statistics":{"A-b":1,"a_b":2,"a-B":3,"a_b":4}}`))
	assert.Equal(t, map[string]float64{"statistics.a_b": 1}, got)
}

func TestMetricsDeepNesting(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: []MetricRule{{Path: "statistics.**"}}})
	deep := `{"statistics":` + strings.Repeat(`{"a":`, 5000) + "1" + strings.Repeat("}", 5000) + "}"
	got := e.extract([]byte(deep))
	assert.Empty(t, got)

	// still finds values within max_depth
	shallow := `{"statistics":` + strings.Repeat(`{"a":`, 5) + "1" + strings.Repeat("}", 5) + "}"
	assert.Equal(t, map[string]float64{"statistics.a.a.a.a.a": 1}, e.extract([]byte(shallow)))
}

func TestMetricsHugeMap(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: []MetricRule{{Path: "statistics.mesh_vpn.groups.*.peers.*.established"}}})
	var b strings.Builder
	b.WriteString(`{"statistics":{"mesh_vpn":{"groups":{"g":{"peers":{`)
	for i := 0; i < 100000; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"p%d":{"established":%d}`, i, i)
	}
	b.WriteString(`}}}}}}`)

	got := e.extract([]byte(b.String()))
	assert.LessOrEqual(t, len(got), metricsDefaultMaxFieldsPerNode)
	assert.NotEmpty(t, got)
}

func TestMetricsLeafLimit(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{MaxLeaves: 10, Rules: []MetricRule{{Path: "statistics.*"}}})
	var b strings.Builder
	b.WriteString(`{"statistics":{`)
	for i := 0; i < 50; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"k%d":%d`, i, i)
	}
	b.WriteString(`}}`)
	got := e.extract([]byte(b.String()))
	assert.Less(t, len(got), 10)
}

func TestMetricsNameLength(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{MaxNameLength: 20, Rules: []MetricRule{{Path: "statistics.**"}}})
	got := e.extract([]byte(`{"statistics":{"short":1,"averyveryverylongname":2}}`))
	assert.Equal(t, map[string]float64{"statistics.short": 1}, got)
}

func TestMetricsGlobalNameCap(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{MaxFieldNames: 5, Rules: []MetricRule{{Path: "statistics.peers.*"}}})
	total := map[string]bool{}
	for n := 0; n < 100; n++ {
		got := e.extract([]byte(fmt.Sprintf(`{"statistics":{"peers":{"node%d":1,"shared":2}}}`, n)))
		for k := range got {
			total[k] = true
		}
	}
	assert.Len(t, total, 5)
	assert.Len(t, e.names, 5)
	// known names still work after the cap
	assert.Contains(t, e.extract([]byte(`{"statistics":{"peers":{"shared":3,"new":1}}}`)), "statistics.peers.shared")
}

func TestMetricsRootMustBeObject(t *testing.T) {
	e := mustExtractor(t, MetricsConfig{Rules: parkerRules})
	for _, in := range []string{`[]`, `1`, `"x"`, `null`, `{"statistics":[1,2]}`, `{"statistics":"x"}`} {
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
	assert.Len(t, rdata.Metrics, 8)

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
	e := mustExtractor(t, MetricsConfig{Rules: []MetricRule{{Path: "statistics.x"}, {Path: "nodeinfo.x"}}})
	rdata, err := (&Response{Raw: deflate(t, []byte(`{"statistics":{"node_id":"aaaaaaaaaaaa","x":1},"nodeinfo":{"node_id":"bbbbbbbbbbbb","x":2}}`))}).parse(nil, e)
	require.NoError(t, err)
	rdata.Nodeinfo = nil
	dropOrphanMetrics(rdata)
	assert.Equal(t, map[string]float64{"statistics.x": 1}, rdata.Metrics)
}

func FuzzExtractMetrics(f *testing.F) {
	f.Add([]byte(parkerStatistics))
	f.Add([]byte(`{"statistics":{"a b":1,"x\n":2,"clat":{"v":1e999}}}`))
	f.Add([]byte(`{"statistics":{"mesh_vpn":{"groups":{"g":{"peers":{"p":{"method":"active"}}}}}}}`))
	f.Add([]byte(`{"statistics":` + strings.Repeat(`{"a":`, 40) + "1" + strings.Repeat("}", 40) + "}"))

	rules := append([]MetricRule{{Path: "statistics.**"}, {Path: "nodeinfo.*.*"}}, parkerRules...)
	f.Fuzz(func(t *testing.T, in []byte) {
		e := mustExtractor(t, MetricsConfig{MaxFieldsPerNode: 64, Rules: rules})
		if !json.Valid(in) {
			return
		}
		got := e.extract(in)
		if len(got) > 64 {
			t.Fatalf("too many fields: %d", len(got))
		}
		for k, v := range got {
			if !validMetricNameRe.MatchString(k) || len(k) > metricsDefaultMaxNameLength {
				t.Fatalf("invalid field name %q", k)
			}
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("non-finite value for %q", k)
			}
		}

		// the full parse path must not panic either
		_, _ = (&Response{Raw: deflate(t, in)}).parse(nil, e)
		_, _ = (&Response{Raw: in}).parse(nil, e)
	})
}
