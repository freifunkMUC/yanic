package influxdb

import (
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"codeberg.org/FreifunkBremen/yanic/lib/log"
	"codeberg.org/FreifunkBremen/yanic/runtime"

	models "github.com/influxdata/influxdb1-client/models"
	client "github.com/influxdata/influxdb1-client/v2"
)

// PruneNodes prunes historical per-node data
func (conn *Connection) PruneNodes(deleteAfter time.Duration) {
	for _, measurement := range []string{MeasurementNode, MeasurementLink} {
		query := fmt.Sprintf("delete from %s where time < now() - %ds", measurement, deleteAfter/time.Second)
		if _, err := conn.client.Query(client.NewQuery(query, conn.config.Database(), "m")); err != nil {
			slog.Error("prune nodes", log.AttrError(err), "database", "influxdb")
		}
	}

}

// InsertNode stores statistics and neighbours in the database
func (conn *Connection) InsertNode(node *runtime.Node) {
	stats := node.Statistics
	time := node.Lastseen.GetTime()

	if stats == nil || stats.NodeID == "" {
		return
	}

	tags := models.Tags{}
	tags.SetString("nodeid", stats.NodeID)
	tags.SetString("is_gateway", strconv.FormatBool(node.IsGateway()))

	fields := models.Fields{
		"load":             stats.LoadAverage,
		"time.up":          int64(stats.Uptime),
		"time.idle":        int64(stats.Idletime),
		"proc.running":     stats.Processes.Running,
		"clients.wifi":     stats.Clients.Wifi,
		"clients.wifi24":   stats.Clients.Wifi24,
		"clients.wifi5":    stats.Clients.Wifi5,
		"clients.owe":      stats.Clients.OWE,
		"clients.owe24":    stats.Clients.OWE24,
		"clients.owe5":     stats.Clients.OWE5,
		"clients.total":    stats.Clients.Total,
		"memory.buffers":   stats.Memory.Buffers,
		"memory.cached":    stats.Memory.Cached,
		"memory.free":      stats.Memory.Free,
		"memory.total":     stats.Memory.Total,
		"memory.available": stats.Memory.Available,
	}

	vpnInterfaces := make(map[string]bool)

	if nodeinfo := node.Nodeinfo; nodeinfo != nil {
		for _, mIface := range nodeinfo.Network.Mesh {
			for _, tunnel := range mIface.Interfaces.Tunnel {
				vpnInterfaces[tunnel] = true
			}
		}

		tags.SetString("hostname", nodeinfo.Hostname)
		if nodeinfo.System.SiteCode != "" {
			tags.SetString("site", nodeinfo.System.SiteCode)
		}
		if nodeinfo.System.DomainCode != "" {
			tags.SetString("domain", nodeinfo.System.DomainCode)
		}
		if owner := nodeinfo.Owner; owner != nil {
			tags.SetString("owner", owner.Contact)
		}
		if wireless := nodeinfo.Wireless; wireless != nil {
			fields["wireless.txpower24"] = wireless.TxPower24
			fields["wireless.txpower5"] = wireless.TxPower5
		}
		// Hardware
		tags.SetString("model", nodeinfo.Hardware.Model)
		fields["nproc"] = nodeinfo.Hardware.Nproc
		if nodeinfo.Software.Firmware != nil {
			tags.SetString("firmware_base", nodeinfo.Software.Firmware.Base)
			tags.SetString("firmware_release", nodeinfo.Software.Firmware.Release)
			tags.SetString("firmware_target", nodeinfo.Software.Firmware.Target)
			tags.SetString("firmware_subtarget", nodeinfo.Software.Firmware.Subtarget)
			tags.SetString("firmware_image_name", nodeinfo.Software.Firmware.ImageName)
		}
		if nodeinfo.Software.Autoupdater != nil && nodeinfo.Software.Autoupdater.Enabled {
			tags.SetString("autoupdater", nodeinfo.Software.Autoupdater.Branch)
		} else {
			tags.SetString("autoupdater", runtime.DISABLED_AUTOUPDATER)
		}

	}
	if neighbours := node.Neighbours; neighbours != nil {
		// VPN Neighbours are Neighbours but includet in one protocol
		vpn := 0

		// protocol: Batman Advance
		batadv := 0
		for mac, batadvNeighbours := range neighbours.Batadv {
			batadv += len(batadvNeighbours.Neighbours)
			if _, ok := vpnInterfaces[mac]; ok {
				vpn += len(batadvNeighbours.Neighbours)
			}
		}
		fields["neighbours.batadv"] = batadv

		// protocol: Babel
		babel := 0
		for _, babelNeighbours := range neighbours.Babel {
			babel += len(babelNeighbours.Neighbours)
			if _, ok := vpnInterfaces[babelNeighbours.LinkLocalAddress]; ok {
				vpn += len(babelNeighbours.Neighbours)
			}
		}
		fields["neighbours.babel"] = babel

		// protocol: LLDP
		lldp := 0
		for _, lldpNeighbours := range neighbours.LLDP {
			lldp += len(lldpNeighbours)
		}
		fields["neighbours.lldp"] = lldp

		// vpn  wait for babel
		fields["neighbours.vpn"] = vpn

		// total is the sum of all protocols
		fields["neighbours.total"] = batadv + babel + lldp
	}
	if procstat := stats.ProcStats; procstat != nil {
		fields["stat.cpu.user"] = procstat.CPU.User
		fields["stat.cpu.nice"] = procstat.CPU.Nice
		fields["stat.cpu.system"] = procstat.CPU.System
		fields["stat.cpu.idle"] = procstat.CPU.Idle
		fields["stat.cpu.iowait"] = procstat.CPU.IOWait
		fields["stat.cpu.irq"] = procstat.CPU.IRQ
		fields["stat.cpu.softirq"] = procstat.CPU.SoftIRQ
		fields["stat.intr"] = procstat.Intr
		fields["stat.ctxt"] = procstat.ContextSwitches
		fields["stat.softirq"] = procstat.SoftIRQ
		fields["stat.processes"] = procstat.Processes
	}

	if t := stats.Traffic.Rx; t != nil {
		fields["traffic.rx.bytes"] = int64(t.Bytes)
		fields["traffic.rx.packets"] = t.Packets
	}
	if t := stats.Traffic.Tx; t != nil {
		fields["traffic.tx.bytes"] = int64(t.Bytes)
		fields["traffic.tx.packets"] = t.Packets
		fields["traffic.tx.dropped"] = t.Dropped
	}
	if t := stats.Traffic.Forward; t != nil {
		fields["traffic.forward.bytes"] = int64(t.Bytes)
		fields["traffic.forward.packets"] = t.Packets
	}
	if t := stats.Traffic.MgmtRx; t != nil {
		fields["traffic.mgmt_rx.bytes"] = int64(t.Bytes)
		fields["traffic.mgmt_rx.packets"] = t.Packets
	}
	if t := stats.Traffic.MgmtTx; t != nil {
		fields["traffic.mgmt_tx.bytes"] = int64(t.Bytes)
		fields["traffic.mgmt_tx.packets"] = t.Packets
	}

	for _, airtime := range stats.Wireless {
		suffix := airtime.FrequencyName()
		fields["airtime"+suffix+".chan_util"] = airtime.ChanUtil
		fields["airtime"+suffix+".rx_util"] = airtime.RxUtil
		fields["airtime"+suffix+".tx_util"] = airtime.TxUtil
		fields["airtime"+suffix+".noise"] = airtime.Noise
		fields["airtime"+suffix+".frequency"] = airtime.Frequency
		tags.SetString("frequency"+suffix, strconv.Itoa(int(airtime.Frequency)))
	}

	addMetrics(fields, node.Metrics)

	conn.addPoint(MeasurementNode, tags, fields, time)

	// Add DHCP statistics
	if dhcp := stats.DHCP; dhcp != nil {
		fields := models.Fields{
			"decline":  dhcp.Decline,
			"offer":    dhcp.Offer,
			"ack":      dhcp.Ack,
			"nak":      dhcp.Nak,
			"request":  dhcp.Request,
			"discover": dhcp.Discover,
			"inform":   dhcp.Inform,
			"release":  dhcp.Release,

			"leases.allocated": dhcp.LeasesAllocated,
			"leases.pruned":    dhcp.LeasesPruned,
		}

		conn.addPoint(MeasurementDHCP, tags, fields, time)
	}
}

// reservedNodeFields are built-in node fields inside the respondd
// namespaces. Generic metrics must never use these names, because a type
// conflict would make InfluxDB reject the points of other nodes.
var reservedNodeFields = map[string]bool{
	"neighbours.batadv": true,
	"neighbours.babel":  true,
	"neighbours.lldp":   true,
	"neighbours.vpn":    true,
	"neighbours.total":  true,
}

// validMetricName accepts only [a-z0-9_] segments joined by dots,
// with a respondd section as the first segment.
func validMetricName(name string) bool {
	root, rest, ok := strings.Cut(name, ".")
	if !ok || rest == "" || (root != "statistics" && root != "nodeinfo" && root != "neighbours") {
		return false
	}
	prevDot := false
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
			prevDot = false
		case c == '.' && !prevDot && i > 0 && i < len(rest)-1:
			prevDot = true
		default:
			return false
		}
	}
	return true
}

// addMetrics adds generic respondd metrics as float fields.
// It never overwrites a built-in field.
func addMetrics(fields models.Fields, metrics map[string]float64) {
	for name, value := range metrics {
		if _, exists := fields[name]; exists || reservedNodeFields[name] || !validMetricName(name) {
			continue
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		fields[name] = value
	}
}
