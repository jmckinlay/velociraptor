package artifacts

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	errors "github.com/pkg/errors"
	"gopkg.in/yaml.v2"
	actions_proto "www.velocidex.com/golang/velociraptor/actions/proto"
	artifacts_proto "www.velocidex.com/golang/velociraptor/artifacts/proto"
	config "www.velocidex.com/golang/velociraptor/config"
	logging "www.velocidex.com/golang/velociraptor/logging"
	utils "www.velocidex.com/golang/velociraptor/utils"
	"www.velocidex.com/golang/vfilter"
)

var (
	artifact_in_query_regex = regexp.MustCompile("Artifact\\.([^\\s\\(]+)\\(")
	global_repository       *Repository
	mu                      sync.Mutex
)

// Holds multiple artifact definitions.
type Repository struct {
	data        map[string]*artifacts_proto.Artifact
	loaded_dirs []string
}

func (self *Repository) LoadDirectory(dirname string) (*int, error) {
	count := 0
	if utils.InString(&self.loaded_dirs, dirname) {
		return &count, nil
	}
	self.loaded_dirs = append(self.loaded_dirs, dirname)
	return &count, filepath.Walk(dirname,
		func(file_path string, info os.FileInfo, err error) error {
			if err != nil {
				return errors.WithStack(err)
			}

			if !info.IsDir() && strings.HasSuffix(info.Name(), ".yaml") {
				artifact, err := Parse(file_path)
				if err != nil {
					return errors.Wrap(err, path.Join(dirname, info.Name()))
				}

				self.data[artifact.Name] = artifact
				count += 1
			}
			return nil
		})
}

func (self *Repository) LoadYaml(data string) error {
	artifact := &artifacts_proto.Artifact{}
	err := yaml.UnmarshalStrict([]byte(data), artifact)
	if err != nil {
		return errors.WithStack(err)
	}
	self.data[artifact.Name] = artifact
	return nil
}

func (self *Repository) Get(name string) (*artifacts_proto.Artifact, bool) {
	res, pres := self.data[name]
	return res, pres
}

func (self *Repository) Set(artifact *artifacts_proto.Artifact) {
	self.data[artifact.Name] = artifact
}

func (self *Repository) List() []string {
	result := []string{}
	for k, _ := range self.data {
		result = append(result, k)
	}
	sort.Strings(result)
	return result
}

// Parse the query and determine if it requires any artifacts. If any
// artifacts are found, then recursive determine their dependencies
// etc.
func (self *Repository) GetQueryDependencies(query string) []string {
	// For now this is really dumb - just search for something
	// that looks like an artifact.
	result := []string{}
	for _, hit := range artifact_in_query_regex.FindAllStringSubmatch(
		query, -1) {
		_, pres := self.data[hit[1]]
		if pres {
			result = append(result, hit[1])
		}
	}

	return result
}

func (self *Repository) PopulateArtifactsVQLCollectorArgs(
	request *actions_proto.VQLCollectorArgs) {
	dependencies := make(map[string]bool)
	for _, query := range request.Query {
		for _, dep := range self.GetQueryDependencies(query.VQL) {
			dependencies[dep] = true
		}
	}
	for k, _ := range dependencies {
		artifact, pres := self.Get(k)
		if pres {
			// Deliberately make a copy of the artifact -
			// we do not want to give away metadata to the
			// client.
			request.Artifacts = append(request.Artifacts,
				&artifacts_proto.Artifact{
					Name:       artifact.Name,
					Parameters: artifact.Parameters,
					Sources:    artifact.Sources,
				})
		}
	}
}

func NewRepository() *Repository {
	return &Repository{
		data: make(map[string]*artifacts_proto.Artifact)}
}

func Parse(filename string) (*artifacts_proto.Artifact, error) {
	result := &artifacts_proto.Artifact{}
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	err = yaml.UnmarshalStrict(data, result)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return result, nil
}

func Compile(artifact *artifacts_proto.Artifact,
	result *actions_proto.VQLCollectorArgs) error {
	for _, parameter := range artifact.Parameters {
		value := parameter.Default
		result.Env = append(result.Env, &actions_proto.VQLEnv{
			Key:   parameter.Name,
			Value: value,
		})
	}
	source_precondition := ""
	for idx, source := range artifact.Sources {
		prefix := fmt.Sprintf("%s_%d", escape_name(artifact.Name), idx)
		source_result := ""
		if source.Precondition != "" {
			source_precondition = "precondition_" + prefix
			result.Query = append(result.Query, &actions_proto.VQLRequest{
				VQL: "LET " + source_precondition + " = " +
					source.Precondition,
			})
		}

		queries := []string{}
		// The artifact format requires all queries to be LET
		// queries except for the last one.
		for idx2, query := range source.Queries {
			// Verify the query's syntax.
			vql, err := vfilter.Parse(query)
			if err != nil {
				return errors.Wrap(
					err, fmt.Sprintf(
						"While parsing source query %d",
						idx2))
			}

			query_name := fmt.Sprintf("%s_%d", prefix, idx2)
			if idx2 < len(source.Queries)-1 {
				if vql.Let == "" {
					return errors.New(
						"Invalid artifact: All Queries in a source " +
							"must be LET queries, except for the " +
							"final one.")
				}
				result.Query = append(result.Query,
					&actions_proto.VQLRequest{
						VQL: query,
					})
			} else {
				if vql.Let != "" {
					return errors.New(
						"Invalid artifact: All Queries in a source " +
							"must be LET queries, except for the " +
							"final one.")
				}

				result.Query = append(result.Query,
					&actions_proto.VQLRequest{
						VQL: "LET " + query_name +
							" = " + query,
					})
				queries = append(queries, query_name)
			}
			source_result = query_name
		}

		if source.Precondition != "" {
			result.Query = append(result.Query, &actions_proto.VQLRequest{
				Name: "Artifact " + artifact.Name,
				VQL: fmt.Sprintf(
					"SELECT * FROM if(then=%s, condition=%s)",
					source_result, source_precondition),
			})
		} else {
			result.Query = append(result.Query, &actions_proto.VQLRequest{
				Name: "Artifact " + artifact.Name,
				VQL:  "SELECT * FROM " + source_result,
			})
		}
	}

	return nil
}

func escape_name(name string) string {
	return regexp.MustCompile("[^a-zA-Z0-9]").ReplaceAllString(name, "_")
}

type init_function func(*config.Config) error

var init_registry []init_function

func GetGlobalRepository(config_obj *config.Config) (*Repository, error) {
	mu.Lock()
	defer mu.Unlock()

	if global_repository != nil {
		return global_repository, nil
	}

	global_repository = NewRepository()

	for _, function := range init_registry {
		err := function(config_obj)
		if err != nil {
			return nil, err
		}
	}

	logger := logging.NewLogger(config_obj)
	if config_obj.Frontend.ArtifactsPath != "" {
		count, err := global_repository.LoadDirectory(
			config_obj.Frontend.ArtifactsPath)
		switch errors.Cause(err).(type) {

		// PathError is not fatal - it means we just
		// cant load the directory.
		case *os.PathError:
			logger.Info("Unable to load artifacts from directory "+
				"%s (skipping): %v", config_obj.Frontend.ArtifactsPath, err)
		case nil:
			break
		default:
			// Other errors are fatal - they mean we cant
			// parse the artifacts themselves.
			return nil, err
		}
		logger.Info("Loaded %d artifacts from %s",
			*count, config_obj.Frontend.ArtifactsPath)
	}
	return global_repository, nil
}
