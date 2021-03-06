package config

import (
	"io/ioutil"
	"os"
	"runtime"

	"github.com/ghodss/yaml"
	errors "github.com/pkg/errors"
	api_proto "www.velocidex.com/golang/velociraptor/api/proto"
	constants "www.velocidex.com/golang/velociraptor/constants"
)

// Embed build time constants into here for reporting client version.
// https://husobee.github.io/golang/compile/time/variables/2015/12/03/compile-time-const.html
var (
	build_time  string
	commit_hash string
)

type Config struct {
	*api_proto.Config
}

// Return the location of the writeback file.
func (self *Config) WritebackLocation() string {
	switch runtime.GOOS {
	case "linux":
		return os.ExpandEnv(self.Client.WritebackLinux)
	case "windows":
		return os.ExpandEnv(self.Client.WritebackWindows)
	default:
		return os.ExpandEnv(self.Client.WritebackLinux)
	}
}

// Create a default configuration object.
func GetDefaultConfig() *Config {
	return &Config{
		&api_proto.Config{
			Version: &api_proto.Version{
				Name:      "velociraptor",
				Version:   constants.VERSION,
				BuildTime: build_time,
				Commit:    commit_hash,
			},
			Client: &api_proto.ClientConfig{
				WritebackLinux: "/etc/velociraptor.writeback.yaml",
				WritebackWindows: "$ProgramFiles\\Velociraptor\\" +
					"velociraptor.writeback.yaml",
				MaxPoll: 600,

				// Specific instructions for the
				// windows service installer.
				WindowsInstaller: &api_proto.WindowsInstallerConfig{
					ServiceName: "Velociraptor",
					InstallPath: "$ProgramFiles\\Velociraptor\\" +
						"Velociraptor.exe",
				},
			},
			API: &api_proto.APIConfig{
				// Bind port for gRPC endpoint - this should not
				// normally be exposed.
				BindAddress: "127.0.0.1",
				BindPort:    8888,
			},
			GUI: &api_proto.GUIConfig{
				// Bind port for GUI. If you expose this on a
				// reachable IP address you must enable TLS!
				BindAddress: "127.0.0.1",
				BindPort:    8889,
				InternalCidr: []string{
					"127.0.0.1/12", "192.168.0.0/16",
				},
			},
			CA: &api_proto.CAConfig{},
			Frontend: &api_proto.FrontendConfig{
				// A public interface for clients to
				// connect to.
				BindAddress:     "0.0.0.0",
				BindPort:        8000,
				ClientLeaseTime: 600,
			},
			Datastore: &api_proto.DatastoreConfig{
				Implementation: "FileBaseDataStore",

				// Users would probably need to change
				// this to something more permanent.
				Location:           "/tmp/velociraptor",
				FilestoreDirectory: "/tmp/velociraptor",
			},
			Flows:     &api_proto.FlowsConfig{},
			Writeback: &api_proto.Writeback{},
			Events: &api_proto.ClientEvents{
				Artifacts: []string{"Generic.Client.Stats"},
				Version:   1,
			},
		},
	}
}

func maybeReadEmbeddedConfig() *Config {
	result := GetDefaultConfig()
	err := yaml.Unmarshal(FileConfigDefaultYaml, result)
	if err != nil {
		return nil
	}

	return result
}

// Load the config stored in the YAML file and returns a config object.
func LoadConfig(filename string) (*Config, error) {
	default_config := GetDefaultConfig()
	result := GetDefaultConfig()

	// If filename is specified we try to read from it.
	if filename != "" {
		data, err := ioutil.ReadFile(filename)
		if err == nil {
			err = yaml.Unmarshal(data, result)
			if err != nil {
				return nil, errors.WithStack(err)
			}

			// TODO: Check if the config version is compatible with our
			// version. We always set the result's version to our version.
			result.Version = default_config.Version

			return result, nil

		}
	}

	// Otherwise we try to read from the embedded config.
	embedded_config := maybeReadEmbeddedConfig()
	if embedded_config == nil {
		return nil, errors.New(
			"No embedded config - try to pack one with the pack command or " +
				"provide the --config flag.")
	}

	embedded_config.Version = default_config.Version
	return embedded_config, nil
}

func LoadClientConfig(filename string) (*Config, error) {
	client_config, err := LoadConfig(filename)
	if err != nil {
		return nil, err
	}

	existing_writeback := &api_proto.Writeback{}
	data, err := ioutil.ReadFile(client_config.WritebackLocation())
	// Failing to read the file is not an error - the file may not
	// exist yet.
	if err == nil {
		err = yaml.Unmarshal(data, existing_writeback)
		if err != nil {
			return nil, errors.WithStack(err)
		}
	}

	// Merge the writeback with the config.
	client_config.Writeback = existing_writeback
	return client_config, nil
}

func WriteConfigToFile(filename string, config *Config) error {
	bytes, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	// Make sure the new file is only readable by root.
	err = ioutil.WriteFile(filename, bytes, 0600)
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}

// Update the client's writeback file.
func UpdateWriteback(config_obj *Config) error {
	if config_obj.WritebackLocation() == "" {
		return nil
	}

	bytes, err := yaml.Marshal(config_obj.Writeback)
	if err != nil {
		return errors.WithStack(err)
	}

	// Make sure the new file is only readable by root.
	err = ioutil.WriteFile(config_obj.WritebackLocation(), bytes, 0600)
	if err != nil {
		return errors.WithStack(err)
	}

	return nil
}
