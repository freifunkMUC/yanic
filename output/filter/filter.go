package filter

import (
	"cmp"
	"fmt"
	"slices"

	"codeberg.org/FreifunkBremen/yanic/lib/log"
	"codeberg.org/FreifunkBremen/yanic/runtime"
)

// factory function for building a filter
// may return nil if the filter never applies
type factory func(interface{}) (Filter, error)

// Filter is a filter instance
type Filter interface {
	Apply(*runtime.Node) *runtime.Node
}

// Set is a list of configured filters
type Set []Filter

var filters = make(map[string]factory)

// filters which rewrite the node, they run before all other filters
var rewriteFilters = []string{"domain_as_site", "domain_append_site"}

func order(name string) int {
	if i := slices.Index(rewriteFilters, name); i >= 0 {
		return i
	}
	return len(rewriteFilters)
}

// Register registers a new filter
func Register(name string, f factory) {
	if _, ok := filters[name]; ok {
		log.Panic("filter already registered", "name", name)
	}
	filters[name] = f
}

// New returns and initializes a set of filters
func New(configs map[string]interface{}) (set Set, errs []error) {
	names := make([]string, 0, len(configs))
	for name := range configs {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int {
		if c := cmp.Compare(order(a), order(b)); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})

	for _, name := range names {
		config := configs[name]
		if config == nil {
			continue
		}

		f := filters[name]
		if f == nil {
			errs = append(errs, fmt.Errorf("unknown filter: %s", name))
		} else if filter, err := f(config); err != nil {
			errs = append(errs, fmt.Errorf("%v: unable to initialize filter %s", err, name))
		} else if filter != nil {
			set = append(set, filter)
		}
	}
	return
}

// Apply applies the filter set to the given node list and returns a new node list
func (set Set) Apply(nodesOrigin *runtime.Nodes) *runtime.Nodes {
	nodes := runtime.NewNodes(&runtime.NodesConfig{})

	nodesOrigin.RLock()
	defer nodesOrigin.RUnlock()

	for _, nodeOrigin := range nodesOrigin.List {
		nodeCopy := *nodeOrigin
		node := &nodeCopy
		for _, filter := range set {
			node = filter.Apply(node)
			if node == nil {
				break
			}
		}

		if node != nil {
			nodes.AddNode(node)
		}
	}
	return nodes
}
