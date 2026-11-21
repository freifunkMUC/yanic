package respond

import (
	"fmt"
	"log/slog"
	"net"
	"time"

	"golang.org/x/sys/unix"

	"codeberg.org/FreifunkBremen/yanic/data"
	"codeberg.org/FreifunkBremen/yanic/database"
	"codeberg.org/FreifunkBremen/yanic/lib/jsontime"
	"codeberg.org/FreifunkBremen/yanic/lib/log"
	"codeberg.org/FreifunkBremen/yanic/runtime"
)

// Collector for a specificle respond messages
type Collector struct {
	connections []multicastConn // UDP sockets

	queue    chan *Response // received responses
	db       database.Connection
	nodes    *runtime.Nodes
	interval time.Duration // Interval for multicast packets
	stop     chan interface{}
	config   *Config
}

type multicastConn struct {
	Conn             *net.UDPConn
	SendRequest      bool
	MulticastAddress net.IP
}

// NewCollector creates a Collector struct
func NewCollector(db database.Connection, nodes *runtime.Nodes, config *Config) *Collector {

	coll := &Collector{
		db:     db,
		nodes:  nodes,
		queue:  make(chan *Response, 400),
		stop:   make(chan interface{}),
		config: config,
	}

	for _, iface := range config.Interfaces {
		coll.listenUDP(iface)
	}

	go coll.parser()

	if coll.db != nil {
		go coll.globalStatsWorker()
	}

	return coll
}

func (coll *Collector) listenUDP(iface InterfaceConfig) {

	var addr net.IP

	ifaceInfo, err := net.InterfaceByName(iface.InterfaceName)

	if err == nil {
		if iface.IPAddress != "" {
			addr = net.ParseIP(iface.IPAddress)
		} else {
			addr, err = getUnicastAddr(ifaceInfo)
		}
	}

	if err != nil {
		log.Panic("error fetch ip address on iface", log.AttrError(err), "iface", iface.InterfaceName)
	}

	multicastAddress := MulticastAddressDefault
	if iface.MulticastAddress != "" {
		multicastAddress = iface.MulticastAddress
	}

	// Open socket
	conn, err := net.ListenUDP("udp", &net.UDPAddr{
		IP:   addr,
		Port: iface.Port,
		Zone: iface.InterfaceName,
	})
	if err != nil {
		log.Panic("listen on udp", log.AttrError(err))
	}
	if err := conn.SetReadBuffer(MaxDataGramSize); err != nil {
		slog.Error("failed to set read buffer", log.AttrError(err))
	}

	raw, err := conn.SyscallConn()
	if err != nil {
		log.Panic("open syscall socket", log.AttrError(err))
	}
	err = raw.Control(func(fd uintptr) {
		err = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_IF, ifaceInfo.Index)
		if err != nil {
			log.Panic("listen multicast on syscall socket", log.AttrError(err))
		}
	})
	if err != nil {
		log.Panic("failed to set syscall control for listen multicast", log.AttrError(err))
	}

	coll.connections = append(coll.connections, multicastConn{
		Conn:             conn,
		SendRequest:      !iface.SendNoRequest,
		MulticastAddress: net.ParseIP(multicastAddress),
	})

	// Start receiver
	go coll.receiver(conn)
}

// Returns a unicast address of given interface (linklocal or global unicast address)
func getUnicastAddr(iface *net.Interface) (net.IP, error) {
	addresses, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	var ip net.IP

	for _, addr := range addresses {
		ipnet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		if (ip == nil && ipnet.IP.IsGlobalUnicast()) || ipnet.IP.IsLinkLocalUnicast() {
			ip = ipnet.IP
		}
	}
	if ip != nil {
		return ip, nil
	}
	return nil, fmt.Errorf("unable to find a unicast address")
}

// Start Collector
func (coll *Collector) Start(interval time.Duration) {
	if coll.interval != 0 {
		log.Panic("already started")
	}
	if interval <= 0 {
		log.Panic("invalid collector interval")
	}
	coll.interval = interval

	go func() {
		coll.sendOnce() // immediately
		coll.sender()   // periodically
	}()
}

// Close Collector
func (coll *Collector) Close() {
	close(coll.stop)
	for _, conn := range coll.connections {
		if err := conn.Conn.Close(); err != nil {
			slog.Error("close during collector sockets", log.AttrError(err))
		}
	}
	close(coll.queue)
}

func (coll *Collector) sendOnce() {
	now := jsontime.Now()
	coll.sendMulticast()

	// Wait for the multicast responses to be processed and send unicasts
	time.Sleep(coll.interval / 2)
	coll.sendUnicasts(now)
}

func (coll *Collector) sendMulticast() {
	slog.Info("sending multicasts")
	for _, conn := range coll.connections {
		if conn.SendRequest {
			coll.sendPacket(conn.Conn, conn.MulticastAddress)
		}
	}
}

// Send unicast packets to nodes that did not answer the multicast
func (coll *Collector) sendUnicasts(seenBefore jsontime.Time) {
	seenAfter := seenBefore.Add(-time.Minute * 10)

	// Select online nodes that has not been seen recently
	nodes := coll.nodes.Select(func(n *runtime.Node) bool {
		return n.Lastseen.After(seenAfter) && n.Lastseen.Before(seenBefore) && n.Address != nil
	})

	// Send unicast packets
	count := 0
	for _, node := range nodes {
		send := 0
		for _, conn := range coll.connections {
			if node.Address.Zone != "" && conn.Conn.LocalAddr().(*net.UDPAddr).Zone != node.Address.Zone && conn.SendRequest {
				continue
			}
			coll.sendPacket(conn.Conn, node.Address.IP)
			send++
		}
		if send == 0 {
			slog.Error("unable to find connection", "iface", node.Address.Zone)
		} else {
			time.Sleep(10 * time.Millisecond)
			count += send
		}
	}
	slog.Info("sending unicast pkg", "pkg_count", count, "nodes_count", len(nodes))
}

// SendPacket sends a UDP request to the given unicast or multicast address on the first UDP socket
func (coll *Collector) SendPacket(destination net.IP) {
	coll.sendPacket(coll.connections[0].Conn, destination)
}

// sendPacket sends a UDP request to the given unicast or multicast address on the given UDP socket
func (coll *Collector) sendPacket(conn *net.UDPConn, destination net.IP) {
	addr := net.UDPAddr{
		IP:   destination,
		Port: PortDefault,
		Zone: conn.LocalAddr().(*net.UDPAddr).Zone,
	}

	if _, err := conn.WriteToUDP([]byte("GET nodeinfo statistics neighbours"), &addr); err != nil {
		slog.Error("WriteToUDP failed", log.AttrError(err), "address", addr.String())
	}
}

// send packets continuously
func (coll *Collector) sender() {
	ticker := time.NewTicker(coll.interval)
	for {
		select {
		case <-coll.stop:
			ticker.Stop()
			return
		case <-ticker.C:
			// send the multicast packet to request per-node statistics
			coll.sendOnce()
		}
	}
}

func (coll *Collector) parser() {
	for obj := range coll.queue {
		if data, err := obj.parse(coll.config.CustomFields); err != nil {
			slog.Error("unable to decode response", log.AttrError(err), "address", obj.Address.String())
		} else {
			coll.saveResponse(obj.Address, data)
		}
	}
}

func (coll *Collector) saveResponse(addr *net.UDPAddr, res *data.ResponseData) {
	// Search for NodeID
	var nodeID string
	if val := res.Nodeinfo; val != nil {
		nodeID = val.NodeID
	} else if val := res.Neighbours; val != nil {
		nodeID = val.NodeID
	} else if val := res.Statistics; val != nil {
		nodeID = val.NodeID
	}

	// Check length of nodeID
	if len(nodeID) != 12 {
		slog.Warn("invalid NodeID", "node_id", nodeID, "address", addr.String())
		return
	}

	// Set fields to nil if nodeID is inconsistent
	if res.Statistics != nil && res.Statistics.NodeID != nodeID {
		res.Statistics = nil
	}
	if res.Neighbours != nil && res.Neighbours.NodeID != nodeID {
		res.Neighbours = nil
	}
	if res.Nodeinfo != nil && res.Nodeinfo.NodeID != nodeID {
		res.Nodeinfo = nil
	}

	// Process the data and update IP address
	node := coll.nodes.Update(nodeID, res)
	coll.nodes.Lock()
	node.Address = addr
	coll.nodes.Unlock()

	// Store statistics in database
	if db := coll.db; db != nil {
		coll.nodes.RLock()
		nodeCopy := *node
		links := coll.nodes.NodeLinks(node)
		coll.nodes.RUnlock()

		db.InsertNode(&nodeCopy)

		// Store link data
		for _, link := range links {
			db.InsertLink(&link, nodeCopy.Lastseen.GetTime())
		}
	}
}

func (coll *Collector) receiver(conn *net.UDPConn) {
	buf := make([]byte, MaxDataGramSize)
	for {
		n, src, err := conn.ReadFromUDP(buf)

		if err != nil {
			if conn != nil {
				slog.Error("ReadFromUDP failed", log.AttrError(err), "local", conn.LocalAddr(), "remote", conn.RemoteAddr())
			} else {
				slog.Error("ReadFromUDP failed", log.AttrError(err))
			}
			return
		}

		raw := make([]byte, n)
		copy(raw, buf)

		coll.queue <- &Response{
			Address: src,
			Raw:     raw,
		}
	}
}

func (coll *Collector) globalStatsWorker() {
	ticker := time.NewTicker(time.Minute)
	for {
		select {
		case <-coll.stop:
			ticker.Stop()
			return
		case <-ticker.C:
			coll.saveGlobalStats()
		}
	}
}

// saves global statistics
func (coll *Collector) saveGlobalStats() {
	stats := runtime.NewGlobalStats(coll.nodes, coll.config.SitesDomains())

	for site, domains := range stats {
		for domain, stat := range domains {
			coll.db.InsertGlobals(stat, time.Now(), site, domain)
		}
	}
}
