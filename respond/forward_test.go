package respond

import (
	"net"
	"testing"
	"time"

	"codeberg.org/FreifunkBremen/yanic/data"
	"codeberg.org/FreifunkBremen/yanic/database/respondd"
	"codeberg.org/FreifunkBremen/yanic/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pointsByMeasurement(points []data.MetricPoint) map[string][]data.MetricPoint {
	m := map[string][]data.MetricPoint{}
	for _, p := range points {
		m[p.Measurement] = append(m[p.Measurement], p)
	}
	return m
}

// A yanic that forwards with the respondd database must keep keys without a Go
// field, so that the receiving yanic can extract metrics from them.
func TestForwardKeepsRawSections(t *testing.T) {
	metrics, err := newMetricsExtractor(&MetricsConfig{
		Enable: true,
		Rules: []MetricRule{
			{Path: "statistics.clat.{direction}", Measurement: "clat"},
			{
				Path:        "statistics.mesh_vpn.groups.{group}.peers.{peer}",
				Measurement: "mesh_vpn_peer",
				Values:      map[string]map[string]float64{"method": {"active": 1, "standby": 0}},
			},
		},
	})
	require.NoError(t, err)

	const in = `{
		"nodeinfo": {"node_id": "deadbeef0001", "hostname": "parker-test"},
		"statistics": {"node_id": "deadbeef0001",
			"clat": {"v4_to_v6": {"packets": 12, "bytes": 3400}, "v6_to_v4": {"packets": 10, "bytes": 2900}},
			"mesh_vpn": {"groups": {"muc01": {"peers": {"pgw01": {"established": 3600, "method": "active"}}}}}}
	}`

	// sending yanic: receive, store, forward (no metrics on this side)
	nodes := runtime.NewNodes(&runtime.NodesConfig{})
	sender := NewCollector(nil, nodes, &Config{})
	defer sender.Close()
	res, err := (&Response{Raw: deflate(t, []byte(in))}).parse(nil, nil)
	require.NoError(t, err)
	sender.saveResponse(&net.UDPAddr{IP: net.IPv6loopback}, res)
	node := nodes.List["deadbeef0001"]
	require.NotNil(t, node)

	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	conn, err := respondd.Connect(map[string]interface{}{"type": "udp", "address": listener.LocalAddr().String()})
	require.NoError(t, err)
	conn.InsertNode(node)
	conn.Close()

	buf := make([]byte, MaxDataGramSize)
	require.NoError(t, listener.SetReadDeadline(time.Now().Add(2*time.Second)))
	n, _, err := listener.ReadFromUDP(buf)
	require.NoError(t, err)

	// receiving yanic: metrics come from the forwarded data
	forwarded, err := (&Response{Raw: buf[:n]}).parse(nil, metrics)
	require.NoError(t, err)
	assert.Equal(t, "parker-test", forwarded.Nodeinfo.Hostname)

	byMeasurement := pointsByMeasurement(forwarded.Metrics)
	assert.Len(t, byMeasurement["clat"], 2)
	require.Len(t, byMeasurement["mesh_vpn_peer"], 1)
	peer := byMeasurement["mesh_vpn_peer"][0]
	assert.Equal(t, map[string]string{"group": "muc01", "peer": "pgw01"}, peer.Tags)
	assert.Equal(t, map[string]float64{"established": 3600, "method": 1}, peer.Fields)
}

// A section with a wrong node_id is dropped, also from the received JSON.
func TestSaveResponseDropsRawOfDiscardedSection(t *testing.T) {
	const in = `{
		"nodeinfo": {"node_id": "deadbeef0001"},
		"statistics": {"node_id": "deadbeef0002", "clat": {"v4_to_v6": {"packets": 1}}}
	}`
	nodes := runtime.NewNodes(&runtime.NodesConfig{})
	coll := NewCollector(nil, nodes, &Config{})
	defer coll.Close()
	res, err := (&Response{Raw: deflate(t, []byte(in))}).parse(nil, nil)
	require.NoError(t, err)
	coll.saveResponse(&net.UDPAddr{IP: net.IPv6loopback}, res)

	node := nodes.List["deadbeef0001"]
	require.NotNil(t, node)
	assert.Contains(t, node.Raw, "nodeinfo")
	assert.NotContains(t, node.Raw, "statistics")
}
