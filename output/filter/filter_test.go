package filter

import (
	"errors"
	"testing"

	"codeberg.org/FreifunkBremen/yanic/data"
	"codeberg.org/FreifunkBremen/yanic/runtime"
	"github.com/stretchr/testify/assert"
)

type filterBool struct{ bool }

func (f filterBool) Apply(node *runtime.Node) *runtime.Node {
	if f.bool {
		return node
	}
	return nil
}

func build(v interface{}) (Filter, error) {
	if config, ok := v.(bool); ok {
		return &filterBool{config}, nil
	}
	return nil, nil
}

func buildNil(v interface{}) (Filter, error) {
	return nil, nil
}

func buildError(v interface{}) (Filter, error) {
	if v != nil {
		return nil, errors.New("some errors")
	}
	return nil, nil
}

func TestFilter(t *testing.T) {
	assert := assert.New(t)

	Register("test_nil", buildNil)
	Register("test_err", buildError)
	Register("test", build)

	// filter still exists
	filter, err := New(map[string]interface{}{
		"adsa": true,
	})
	assert.Len(err, 1)
	assert.Nil(filter)

	// no filter
	filter, err = New(map[string]interface{}{
		"test_nil": 3,
	})
	assert.Len(err, 0)
	assert.Len(filter, 0)

	// filter error
	filter, err = New(map[string]interface{}{
		"test_err": false,
	})
	assert.Len(err, 1)
	assert.Nil(filter)

	// filter a node
	nodes := &runtime.Nodes{
		List: map[string]*runtime.Node{
			"a": {
				Nodeinfo: &data.Nodeinfo{NodeID: "a"},
			},
		},
	}
	filter, err = New(map[string]interface{}{
		"test": false,
	})
	assert.Len(err, 0)
	nodes = filter.Apply(nodes)
	assert.Len(nodes.List, 0)

	// keep a node
	nodes = &runtime.Nodes{
		List: map[string]*runtime.Node{
			"a": {
				Nodeinfo: &data.Nodeinfo{NodeID: "a"},
			},
		},
	}
	filter, err = New(map[string]interface{}{
		"test": true,
	})
	assert.Len(err, 0)
	nodes = filter.Apply(nodes)
	assert.Len(nodes.List, 1)
}

func TestApplyConcurrentUpdate(t *testing.T) {
	nodes := runtime.NewNodes(&runtime.NodesConfig{})
	response := func() *data.ResponseData {
		return &data.ResponseData{
			Nodeinfo:   &data.Nodeinfo{NodeID: "a"},
			Statistics: &data.Statistics{NodeID: "a"},
		}
	}
	nodes.Update("a", response())

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			nodes.Update("a", response())
		}
	}()

	var set Set
	for i := 0; i < 1000; i++ {
		for _, node := range set.Apply(nodes).List {
			_ = node.Online
			_ = node.Statistics
		}
	}
	<-done
}
