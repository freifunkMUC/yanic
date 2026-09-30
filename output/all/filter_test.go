package all

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"codeberg.org/FreifunkBremen/yanic/data"
	"codeberg.org/FreifunkBremen/yanic/output/filter"
	"codeberg.org/FreifunkBremen/yanic/runtime"
)

func TestFilterOrder(t *testing.T) {
	assert := assert.New(t)

	for i := 0; i < 100; i++ {
		set, errs := filter.New(map[string]interface{}{
			"sites":          []interface{}{"city"},
			"domain_as_site": true,
		})
		assert.Len(errs, 0)

		nodes := runtime.NewNodes(&runtime.NodesConfig{})
		nodes.AddNode(&runtime.Node{
			Nodeinfo: &data.Nodeinfo{
				NodeID: "a",
				System: data.System{SiteCode: "ffhb", DomainCode: "city"},
			},
		})

		nodes = set.Apply(nodes)
		if assert.Len(nodes.List, 1) {
			assert.Equal("city", nodes.List["a"].Nodeinfo.System.SiteCode)
		}
	}
}
