package main

import (
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"sync"

	"codeberg.org/FreifunkBremen/yanic/lib/log"
	meshviewerFFRGB "codeberg.org/FreifunkBremen/yanic/output/meshviewer-ffrgb"
	"github.com/digineo/go-ping"
)

func pingNode(pinger *ping.Pinger, node *meshviewerFFRGB.Node, addrStr string) bool {
	logNode := slog.With("node_id", node.NodeID)

	addr, err := net.ResolveIPAddr("ip6", addrStr)
	if err != nil {
		logNode.Warn("error parse ip address for ping", log.AttrError(err))
	}

	if addrStr[:5] == "fe80:" {
		if iface == "" {
			logNode.Debug("skip ll-addr")
			return false
		}
		addr.Zone = iface
	}
	logNode = logNode.With("addr", addr.String())

	_, err = pinger.PingAttempts(addr, pingTimeout, pingCount)

	logNode.Debug("pong", "success", err == nil)
	return err == nil
}

func run(pinger *ping.Pinger) {
	status := &Status{NodesCrashed: []*Node{}}
	var meshviewerjson meshviewerFFRGB.Meshviewer

	if meshviewerURL[:4] == "http" {
		if err := JSONRequest(meshviewerURL, &meshviewerjson); err != nil {
			status.Error = err.Error()
			slog.Error("error during fetch meshviewer.json", log.AttrError(err))
		}
	} else {
		meshviewerFile, err := os.Open(meshviewerURL)
		if err != nil {
			status.Error = err.Error()
			slog.Error("error during fetch meshviewer.json", log.AttrError(err))
		} else if err := json.NewDecoder(meshviewerFile).Decode(&meshviewerjson); err != nil {
			status.Error = err.Error()
			slog.Error("error during decode meshviewer.json", log.AttrError(err))
		}
	}

	slog.Debug("fetched meshviewer.json")

	wg := sync.WaitGroup{}
	wg.Add(len(meshviewerjson.Nodes))

	offline := 0
	for _, node := range meshviewerjson.Nodes {
		go func(node *meshviewerFFRGB.Node) {
			defer wg.Done()
			if node.IsOnline {
				return
			}
			logNode := slog.With("node", node.NodeID)
			wgNode := sync.WaitGroup{}
			wgNode.Add(len(node.Addresses))
			offline += 1
			notReachable := true
			for _, addr := range node.Addresses {
				go func(node *meshviewerFFRGB.Node, addr string) {
					if ok := pingNode(pinger, node, addr); ok {
						notReachable = false
					}
					wgNode.Done()
				}(node, addr)
			}
			wgNode.Wait()
			if !notReachable {
				logNode.Info("add to crashed list")
				status.AddNode(node)
			}
		}(node)
	}

	wg.Wait()

	status.Lock()
	status.NodesCount = len(meshviewerjson.Nodes)
	status.NodesOfflineCount = offline
	status.Unlock()

	tmpFile := statusPath + ".tmp"
	statusFile, err := os.OpenFile(tmpFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		slog.Warn("unable to open status file", log.AttrError(err))
	}
	defer statusFile.Close()

	if err := json.NewEncoder(statusFile).Encode(status); err != nil {
		slog.Warn("unable to write status json", log.AttrError(err))
	}
	if err := os.Rename(tmpFile, statusPath); err != nil {
		slog.Warn("unable to move status file", log.AttrError(err))
	}

	slog.Info("test complete",
		"count_meshviewer", status.NodesCount,
		"count_offline", status.NodesOfflineCount,
		"count_status", len(status.NodesCrashed),
	)
}
