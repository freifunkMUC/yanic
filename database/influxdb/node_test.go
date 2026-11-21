package influxdb

import (
	"math"
	"testing"

	"codeberg.org/FreifunkBremen/yanic/data"
	"codeberg.org/FreifunkBremen/yanic/runtime"

	"github.com/influxdata/influxdb1-client/v2"
	"github.com/stretchr/testify/assert"
)

func TestToInflux(t *testing.T) {
	assert := assert.New(t)

	node := &runtime.Node{
		Online: true,
		Statistics: &data.Statistics{
			NodeID:      "deadbeef",
			LoadAverage: 0.5,
			ProcStats: &data.ProcStats{
				CPU: data.ProcStatsCPU{
					User: 1,
				},
				ContextSwitches: 3,
			},
			Wireless: data.WirelessStatistics{
				&data.WirelessAirtime{Frequency: 5500},
			},
			Traffic: struct {
				Tx      *data.Traffic `json:"tx"`
				Rx      *data.Traffic `json:"rx"`
				Forward *data.Traffic `json:"forward"`
				MgmtTx  *data.Traffic `json:"mgmt_tx"`
				MgmtRx  *data.Traffic `json:"mgmt_rx"`
			}{
				Tx:      &data.Traffic{Dropped: 1321},
				Rx:      &data.Traffic{Bytes: 1213},
				Forward: &data.Traffic{Bytes: 1322},
				MgmtTx:  &data.Traffic{Packets: 2327},
				MgmtRx:  &data.Traffic{Bytes: 2331},
			},
		},
		Nodeinfo: &data.Nodeinfo{
			NodeID: "deadbeef",
			Owner: &data.Owner{
				Contact: "nobody",
			},
			System: data.System{
				SiteCode:   "ffhb",
				DomainCode: "city",
			},
			Wireless: &data.Wireless{
				TxPower24: 3,
				Channel24: 4,
			},
			Network: data.Network{
				Mac: "DEADMAC",
				Mesh: map[string]*data.NetworkInterface{
					"bat0": {
						Interfaces: struct {
							Wireless []string `json:"wireless,omitempty"`
							Other    []string `json:"other,omitempty"`
							Tunnel   []string `json:"tunnel,omitempty"`
						}{
							Tunnel: []string{"a-interface-mac", "fe80::1"},
						},
					},
				},
			},
			Software: data.Software{
				Autoupdater: &struct {
					Enabled bool   `json:"enabled,omitempty"`
					Branch  string `json:"branch,omitempty"`
				}{
					Enabled: true,
					Branch:  "testing",
				},
			},
		},
		Neighbours: &data.Neighbours{
			NodeID: "deadbeef",
			Batadv: map[string]data.BatadvNeighbours{
				"a-interface-mac": {
					Neighbours: map[string]data.BatmanLink{
						"BAFF1E5": {
							TQ: 204,
						},
					},
				},
			},
			Babel: map[string]data.BabelNeighbours{
				"wg-01": {
					LinkLocalAddress: "fe80::1",
					Neighbours: map[string]data.BabelLink{
						"fe80::2": {
							Cost: 0,
						},
					},
				},
			},
			LLDP: map[string][]string{
				"b-interface-mac": {},
			},
		},
	}

	neighbour := &runtime.Node{
		Online: true,
		Nodeinfo: &data.Nodeinfo{
			NodeID: "foobar",
			Network: data.Network{
				Mac: "BAFF1E5",
			},
			Software: data.Software{
				Autoupdater: &struct {
					Enabled bool   `json:"enabled,omitempty"`
					Branch  string `json:"branch,omitempty"`
				}{
					Enabled: false,
				},
				Firmware: &struct {
					Base      string `json:"base,omitempty"`
					Release   string `json:"release,omitempty"`
					Target    string `json:"target,omitempty"`
					Subtarget string `json:"subtarget,omitempty"`
					ImageName string `json:"image_name,omitempty"`
				}{
					Base:      "gluon",
					Target:    "x86",
					Subtarget: "64",
					ImageName: "x86-64",
				},
			},
		},
		Statistics: &data.Statistics{
			NodeID: "foobar",
		},
	}

	// do not add a empty statistics of a node
	droppednode := &runtime.Node{
		Online: true,
		Nodeinfo: &data.Nodeinfo{
			NodeID: "notfound",
			Network: data.Network{
				Mac: "instats",
			},
		},
		Statistics: &data.Statistics{},
	}

	points := testPoints(node, neighbour, droppednode)
	var fields map[string]interface{}
	var tags map[string]string

	assert.Len(points, 3)

	// first point contains the neighbour
	sPoint := points[0]
	tags = sPoint.Tags()
	fields, _ = sPoint.Fields()

	assert.EqualValues("deadbeef", tags["nodeid"])
	assert.EqualValues("nobody", tags["owner"])
	assert.EqualValues("testing", tags["autoupdater"])
	assert.EqualValues("ffhb", tags["site"])
	assert.EqualValues("city", tags["domain"])
	assert.EqualValues(0.5, fields["load"])
	assert.EqualValues(0, fields["neighbours.lldp"])
	assert.EqualValues(1, fields["neighbours.babel"])
	assert.EqualValues(1, fields["neighbours.batadv"])
	assert.EqualValues(2, fields["neighbours.vpn"])
	assert.EqualValues(2, fields["neighbours.total"])

	assert.EqualValues(uint32(3), fields["wireless.txpower24"])
	assert.EqualValues(uint32(5500), fields["airtime11a.frequency"])
	assert.EqualValues("", tags["frequency5500"])

	assert.EqualValues(int64(1213), fields["traffic.rx.bytes"])
	assert.EqualValues(float64(1321), fields["traffic.tx.dropped"])
	assert.EqualValues(int64(1322), fields["traffic.forward.bytes"])
	assert.EqualValues(int64(2331), fields["traffic.mgmt_rx.bytes"])
	assert.EqualValues(float64(2327), fields["traffic.mgmt_tx.packets"])

	// second point contains the link
	nPoint := points[1]
	tags = nPoint.Tags()
	fields, _ = nPoint.Fields()
	assert.EqualValues("link", nPoint.Name())
	assert.EqualValues(map[string]string{
		"source.id":   "deadbeef",
		"source.addr": "a-interface-mac",
		"target.id":   "foobar",
		"target.addr": "BAFF1E5",
		"type":        "vpn",
	}, tags)
	assert.EqualValues(80, fields["tq"])

	// third point contains the neighbour
	nPoint = points[2]
	tags = nPoint.Tags()
	assert.EqualValues("disabled", tags["autoupdater"])
}

// Processes data and returns the InfluxDB points
func testPoints(nodes ...*runtime.Node) (points []*client.Point) {
	// Create dummy client
	influxClient, err := client.NewHTTPClient(client.HTTPConfig{Addr: "http://127.0.0.1"})
	if err != nil {
		panic(err)
	}

	nodesList := runtime.NewNodes(&runtime.NodesConfig{})

	// Create dummy connection
	conn := &Connection{
		points: make(chan *client.Point),
		client: influxClient,
	}

	for _, node := range nodes {
		nodesList.AddNode(node)
	}

	// Process data
	go func() {
		for _, node := range nodes {
			conn.InsertNode(node)
			if node.Neighbours != nil {
				for _, link := range nodesList.NodeLinks(node) {
					conn.InsertLink(&link, node.Lastseen.GetTime())
				}
			}
		}
		conn.Close()
	}()

	// Read points
	for point := range conn.points {
		points = append(points, point)
	}

	return
}

func TestToInfluxMetrics(t *testing.T) {
	assert := assert.New(t)

	node := &runtime.Node{
		Statistics: &data.Statistics{NodeID: "deadbeef", Clients: data.Clients{Total: 3}},
		Nodeinfo:   &data.Nodeinfo{NodeID: "deadbeef", Hostname: "node1", System: data.System{SiteCode: "ffmuc"}},
		Metrics: []data.MetricPoint{
			{Measurement: "mesh_vpn_peer", Tags: map[string]string{"group": "muc01", "peer": "pgw-01"}, Fields: map[string]float64{"established": 3600, "method": 1}},
			{Measurement: "clat", Tags: map[string]string{"direction": "v4_to_v6"}, Fields: map[string]float64{"packets": 1200, "bad name": 1, "nan": math.NaN(), "inf": math.Inf(1)}},
			// all of these points are dropped
			{Measurement: "node", Fields: map[string]float64{"clients.total": 999}},
			{Measurement: "x\nnode", Fields: map[string]float64{"v": 1}},
			{Measurement: "x", Tags: map[string]string{"peer": "a b"}, Fields: map[string]float64{"v": 1}},
			{Measurement: "x", Tags: map[string]string{"peer": "a\nnode,nodeid=evil v=1"}, Fields: map[string]float64{"v": 1}},
			{Measurement: "x", Tags: map[string]string{"nodeid": "evil"}, Fields: map[string]float64{"v": 1}},
			{Measurement: "x", Tags: map[string]string{"Bad": "a"}, Fields: map[string]float64{"v": 1}},
			{Measurement: "x", Fields: map[string]float64{"nan": math.NaN()}},
			{Measurement: "x", Fields: map[string]float64{"a\nb": 1, "a,b": 1, "a=b": 1, "a..b": 1, ".a": 1}},
		},
	}

	points := testPoints(node)
	assert.Len(points, 3)

	nodeFields, _ := points[0].Fields()
	assert.Equal("node", points[0].Name())
	assert.EqualValues(3, nodeFields["clients.total"])

	assert.Equal("mesh_vpn_peer", points[1].Name())
	assert.Equal(map[string]string{"nodeid": "deadbeef", "hostname": "node1", "site": "ffmuc", "group": "muc01", "peer": "pgw-01"}, points[1].Tags())
	fields, _ := points[1].Fields()
	assert.Equal(map[string]interface{}{"established": 3600.0, "method": 1.0}, fields)

	assert.Equal("clat", points[2].Name())
	fields, _ = points[2].Fields()
	assert.Equal(map[string]interface{}{"packets": 1200.0}, fields)

	for _, p := range points {
		assert.NotContains(p.String(), "\n")
		assert.NotContains(p.String(), "evil")
	}
}

func TestMetricsReservedMeasurements(t *testing.T) {
	for _, m := range []string{MeasurementLink, MeasurementNode, MeasurementDHCP, MeasurementGlobal,
		CounterMeasurementFirmware, CounterMeasurementModel, CounterMeasurementAutoupdater} {
		assert.False(t, data.ValidMetricsMeasurement(m), m)
	}
}
