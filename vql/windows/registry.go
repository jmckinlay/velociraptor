// +build windows

// VQL plugins handy for registry parsing.
package windows

import (
	"context"

	glob "www.velocidex.com/golang/velociraptor/glob"
	vql_subsystem "www.velocidex.com/golang/velociraptor/vql"
	vfilter "www.velocidex.com/golang/vfilter"
)

type ReadKeyValuesArgs struct {
	Globs    []string `vfilter:"required,field=globs"`
	Accessor string   `vfilter:"optional,field=accessor"`
}

type ReadKeyValues struct{}

func (self ReadKeyValues) Call(
	ctx context.Context,
	scope *vfilter.Scope,
	args *vfilter.Dict) <-chan vfilter.Row {
	globber := make(glob.Globber)
	output_chan := make(chan vfilter.Row)
	arg := &ReadKeyValuesArgs{}
	err := vfilter.ExtractArgs(scope, args, arg)
	if err != nil {
		scope.Log("read_reg_key: %s", err.Error())
		close(output_chan)
		return output_chan
	}

	accessor_name := arg.Accessor
	if accessor_name == "" {
		accessor_name = "reg"
	}

	accessor := glob.GetAccessor(accessor_name, ctx)
	root := ""
	for _, item := range arg.Globs {
		item_root, item_path, _ := accessor.GetRoot(item)
		if root != "" && root != item_root {
			scope.Log("glob: %s: Must use the same root for "+
				"all globs. Skipping.", item)
			continue
		}
		root = item_root
		globber.Add(item_path, accessor.PathSplit())
	}
	go func() {
		defer close(output_chan)
		file_chan := globber.ExpandWithContext(
			ctx, root, accessor)
		for {
			select {
			case <-ctx.Done():
				return

			case f, ok := <-file_chan:
				if !ok {
					return
				}
				if f.IsDir() {
					res := vfilter.NewDict().
						SetDefault(&vfilter.Null{}).
						SetCaseInsensitive().
						Set("Key", f)
					values, err := accessor.ReadDir(f.FullPath())
					if err != nil {
						continue
					}

					for _, item := range values {
						value_info, ok := item.(glob.FileInfo)
						if ok {
							value_data, ok := value_info.Data().(*vfilter.Dict)
							if ok {
								value, pres := value_data.Get("value")
								if pres {
									res.Set(item.Name(), value)
								}
							}
						}
					}
					output_chan <- res
				}
			}
		}
	}()

	return output_chan
}

func (self ReadKeyValues) Info(type_map *vfilter.TypeMap) *vfilter.PluginInfo {
	return &vfilter.PluginInfo{
		Name: "read_reg_key",
		Doc: "This is a convenience function for reading the entire " +
			"registry key matching the globs. Emits dicts with keys " +
			"being the value names and the values being the value data.",
		ArgType: type_map.AddType(&ReadKeyValuesArgs{}),
	}
}

func init() {
	vql_subsystem.RegisterPlugin(&ReadKeyValues{})
}
