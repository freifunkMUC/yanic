package meshviewer

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/FreifunkBremen/yanic/data"
	"github.com/FreifunkBremen/yanic/runtime"
)

type TestNode struct {
	Nodeinfo   *data.Nodeinfo   `json:"nodeinfo"`
	Neighbours *data.Neighbours `json:"neighbours"`
}

func TestGenerateGraph(t *testing.T) {
	assert := assert.New(t)
	nodes := testGetNodesByFile("node1.json", "node2.json", "node3.json", "node4.json")

	graph := BuildGraph(nodes)
	assert.NotNil(graph)
	assert.Equal(1, graph.Version, "Wrong Version")
	assert.NotNil(graph.Batadv, "no Batadv")
	assert.Equal(false, graph.Batadv.Directed, "directed batadv")
	assert.Len(graph.Batadv.Links, 3, "wrong Links count")
	assert.Equal(4, testNodesCountWithLinks(graph.Batadv.Links), "wrong unneed nodes in graph")
	assert.Len(graph.Batadv.Nodes, 4, "wrong Nodes count")
	// TODO more tests required

	// All Batman IV links carry tq, never throughput
	for _, link := range graph.Batadv.Links {
		assert.NotZero(link.TQ, "Batman IV link missing tq")
		assert.Zero(link.Throughput, "Batman IV link unexpectedly has throughput")
	}
}

func TestGenerateGraphBatmanV(t *testing.T) {
	assert := assert.New(t)
	nodes := testGetNodesByFile("node5.json", "node6.json")

	// mark both nodes online so addLink is reached for both directions
	for _, n := range nodes.List {
		n.Online = true
	}

	graph := BuildGraph(nodes)
	assert.NotNil(graph)
	assert.Len(graph.Batadv.Links, 1, "wrong Links count")

	link := graph.Batadv.Links[0]
	assert.Zero(link.TQ, "Batman V link unexpectedly has tq")
	// Bidirectional throughput: min of 12000 and 24000 = 12000
	assert.Equal(float32(12000), link.Throughput, "wrong throughput on Batman V link")
	assert.True(link.Bidirect, "Batman V link should be bidirectional")
}

func testGetNodesByFile(files ...string) *runtime.Nodes {

	nodes := runtime.NewNodes(&runtime.NodesConfig{})

	for _, file := range files {
		node := testGetNodeByFile(file)
		nodes.Update(file, &data.ResponseData{
			Nodeinfo:   node.Nodeinfo,
			Neighbours: node.Neighbours,
		})
	}

	return nodes
}

func testGetNodeByFile(filename string) *runtime.Node {
	testnode := &TestNode{}
	testfile(filename, testnode)
	return &runtime.Node{
		Nodeinfo:   testnode.Nodeinfo,
		Neighbours: testnode.Neighbours,
	}
}

func testfile(name string, obj interface{}) {
	file, err := os.ReadFile("../../runtime/testdata/" + name)
	if err != nil {
		panic(err)
	}

	if err := json.Unmarshal(file, obj); err != nil {
		panic(err)
	}
}

func testNodesCountWithLinks(links []*GraphLink) int {
	indexMap := make(map[int]bool)
	for _, l := range links {
		indexMap[l.Source] = true
		indexMap[l.Target] = true
	}
	return len(indexMap)
}
