package all

import (
	"fmt"
	"log/slog"

	"codeberg.org/FreifunkBremen/yanic/output"
	"codeberg.org/FreifunkBremen/yanic/output/filter"
	"codeberg.org/FreifunkBremen/yanic/runtime"
)

type Output struct {
	output.Output
	list         map[int]output.Output
	outputFilter map[int]filter.Set
}

type ErrOutputTypeFromFormat struct {
	outputType      string
	configForOutput interface{}
}

func (e *ErrOutputTypeFromFormat) Error() string {
	return fmt.Sprintf("the output type '%s' has the wrong format: read format %T should be an []map[string]interface{}", e.outputType, e.configForOutput)
}

func Register(configuration map[string]interface{}) (output.Output, error) {
	list := make(map[int]output.Output)
	outputFilter := make(map[int]filter.Set)
	i := 1
	allOutputs := configuration
	for outputType, outputRegister := range output.Adapters {
		configForOutput := allOutputs[outputType]
		if configForOutput == nil {
			slog.Info("no configuration found", "output", outputType)
			continue
		}
		outputConfigs, ok := configForOutput.([]map[string]interface{})
		if !ok {
			return nil, &ErrOutputTypeFromFormat{outputType: outputType, configForOutput: configForOutput}
		}
		for _, config := range outputConfigs {
			if c, ok := config["enable"].(bool); ok && !c {
				continue
			}
			output, err := outputRegister(config)
			if err != nil {
				return nil, err
			}
			if output == nil {
				continue
			}
			var errs []error
			var filterSet filter.Set
			if c := config["filter"]; c != nil {
				if filterConf, ok := c.(map[string]interface{}); ok {
					filterSet, errs = filter.New(filterConf)
				}
				if len(errs) > 0 {
					return nil, fmt.Errorf("filter configuration errors: %v", errs)
				}
				outputFilter[i] = filterSet
			}
			list[i] = output
			i++
		}
	}
	return &Output{list: list, outputFilter: outputFilter}, nil
}

func (o *Output) Save(nodes *runtime.Nodes) {
	for i, item := range o.list {
		item.Save(o.outputFilter[i].Apply(nodes))
	}
}
