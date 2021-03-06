package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"path"

	"github.com/ghodss/yaml"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
	api_proto "www.velocidex.com/golang/velociraptor/api/proto"
	config "www.velocidex.com/golang/velociraptor/config"
	"www.velocidex.com/golang/velociraptor/crypto"
	"www.velocidex.com/golang/velociraptor/executor"
	"www.velocidex.com/golang/velociraptor/http_comms"
)

var (
	pool_client_command = app.Command(
		"pool_client", "Run a pool client for load testing.")

	pool_client_number = pool_client_command.Flag(
		"number", "Total number of clients to run.").Int()

	pool_client_writeback_dir = pool_client_command.Flag(
		"writeback_dir", "The directory to store all writebacks.").Default(".").
		ExistingDir()
)

func doPoolClient() {

	ctx := context.Background()
	number_of_clients := *pool_client_number
	if number_of_clients <= 0 {
		number_of_clients = 2
	}

	for i := 0; i < number_of_clients; i++ {
		client_config, err := config.LoadConfig(*config_path)
		kingpin.FatalIfError(err, "Unable to load config file")

		client_config.Client.WritebackLinux = path.Join(
			*pool_client_writeback_dir,
			fmt.Sprintf("pool_client.yaml.%d", i))

		client_config.Client.WritebackWindows = client_config.Client.WritebackLinux

		existing_writeback := &api_proto.Writeback{}
		data, err := ioutil.ReadFile(client_config.WritebackLocation())

		// Failing to read the file is not an error - the file may not
		// exist yet.
		if err == nil {
			err = yaml.Unmarshal(data, existing_writeback)
			kingpin.FatalIfError(err, "Unable to load config file")
		}

		// Merge the writeback with the config.
		client_config.Writeback = existing_writeback

		// Make sure the config is ok.
		err = crypto.VerifyConfig(client_config)
		if err != nil {
			kingpin.FatalIfError(err, "Invalid config")
		}

		manager, err := crypto.NewClientCryptoManager(
			client_config, []byte(client_config.Writeback.PrivateKey))
		kingpin.FatalIfError(err, "Unable to parse config file")

		exe, err := executor.NewClientExecutor(client_config)
		kingpin.FatalIfError(err, "Can not create executor.")

		comm, err := http_comms.NewHTTPCommunicator(
			client_config,
			manager,
			exe,
			client_config.Client.ServerUrls,
		)
		kingpin.FatalIfError(err, "Can not create HTTPCommunicator.")

		// Run the client in the background.
		go comm.Run(ctx)
	}

	// Block forever.
	select {
	case <-ctx.Done():
		break
	}
}

func init() {
	command_handlers = append(command_handlers, func(command string) bool {
		switch command {
		case "pool_client":
			doPoolClient()
		default:
			return false
		}
		return true
	})
}
