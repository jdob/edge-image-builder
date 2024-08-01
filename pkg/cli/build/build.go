package build

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/suse-edge/edge-image-builder/pkg/cli/cmd"
	"github.com/suse-edge/edge-image-builder/pkg/eib"
	"github.com/suse-edge/edge-image-builder/pkg/image"
	"github.com/suse-edge/edge-image-builder/pkg/log"
	"github.com/suse-edge/edge-image-builder/pkg/version"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

const (
	buildLogFilename     = "eib-build.log"
	checkBuildLogMessage = "Please check the eib-build.log file under the build directory for more information."
)

func Run(_ *cli.Context) error {
	args := &cmd.BuildArgs

	rootBuildDir := args.RootBuildDir
	if rootBuildDir == "" {
		const defaultBuildDir = "_build"

		rootBuildDir = filepath.Join(args.ConfigDir, defaultBuildDir)
		if err := os.MkdirAll(rootBuildDir, os.ModePerm); err != nil {
			log.Auditf("The root build directory could not be set up under the configuration directory '%s'.", args.ConfigDir)
			return err
		}
	}

	buildDir, err := eib.SetupBuildDirectory(rootBuildDir)
	if err != nil {
		log.Audit("The build directory could not be set up.")
		return err
	}

	// This needs to occur as early as possible so that the subsequent calls can use the log
	log.ConfigureGlobalLogger(filepath.Join(buildDir, buildLogFilename))

	if cmdErr := imageConfigDirExists(args.ConfigDir); cmdErr != nil {
		cmd.LogError(cmdErr, checkBuildLogMessage)
		os.Exit(1)
	}

	imageDefinition, cmdErr := parseImageDefinition(args.ConfigDir, args.DefinitionFile)
	if cmdErr != nil {
		cmd.LogError(cmdErr, checkBuildLogMessage)
		os.Exit(1)
	}

	combustionDir, artefactsDir, err := eib.SetupCombustionDirectory(buildDir)
	if err != nil {
		log.Auditf("Setting up the combustion directory failed. %s", checkBuildLogMessage)
		zap.S().Fatalf("Failed to create combustion directories: %s", err)
	}

	artifactSources, err := parseArtifactSources()
	if err != nil {
		log.Auditf("Loading artifact sources metadata failed. %s", checkBuildLogMessage)
		zap.S().Fatalf("Parsing artifact sources failed: %v", err)
	}

	ctx := buildContext(buildDir, combustionDir, artefactsDir, args.ConfigDir, imageDefinition, artifactSources)

	if cmdErr = validateImageDefinition(ctx); cmdErr != nil {
		cmd.LogError(cmdErr, checkBuildLogMessage)
		os.Exit(1)
	}

	defer func() {
		if r := recover(); r != nil {
			log.Auditf("Build failed unexpectedly. %s", checkBuildLogMessage)
			zap.S().Fatalf("Unexpected error occurred: %s", r)
		}
	}()

	if err = eib.Run(ctx, rootBuildDir); err != nil {
		log.Audit(checkBuildLogMessage)
		zap.S().Fatalf("An error occurred building the image: %s", err)
	}

	return nil
}

func imageConfigDirExists(configDir string) *cmd.Error {
	_, err := os.Stat(configDir)
	if err == nil {
		return nil
	}

	if errors.Is(err, fs.ErrNotExist) {
		return &cmd.Error{
			UserMessage: fmt.Sprintf("The specified image configuration directory '%s' could not be found.", configDir),
		}
	}

	return &cmd.Error{
		UserMessage: fmt.Sprintf("Unable to check the filesystem for the image configuration directory '%s'.", configDir),
		LogMessage:  fmt.Sprintf("Reading image config dir failed: %v", err),
	}
}

func parseImageDefinition(configDir, definitionFile string) (*image.Definition, *cmd.Error) {
	definitionFilePath := filepath.Join(configDir, definitionFile)

	configData, err := os.ReadFile(definitionFilePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &cmd.Error{
				UserMessage: fmt.Sprintf("The specified definition file '%s' could not be found.", definitionFilePath),
			}
		}

		return nil, &cmd.Error{
			UserMessage: fmt.Sprintf("The specified definition file '%s' could not be read.", definitionFilePath),
			LogMessage:  fmt.Sprintf("Reading definition file failed: %v", err),
		}
	}

	imageDefinition, err := image.ParseDefinition(configData)
	if err != nil {
		if errors.Is(err, image.ErrorInvalidSchemaVersion) {
			m := "Invalid schema version specified. This version of Edge Image Builder supports the following schema versions: %s"
			msg := fmt.Sprintf(m, strings.Join(version.SupportedSchemaVersions, ", "))
			return nil, &cmd.Error{
				UserMessage: msg,
				LogMessage:  msg,
			}
		}

		return nil, &cmd.Error{
			UserMessage: fmt.Sprintf("The image definition file '%s' could not be parsed.", definitionFilePath),
			LogMessage:  fmt.Sprintf("Parsing definition file failed: %v", err),
		}
	}

	return imageDefinition, nil
}

func parseArtifactSources() (*image.ArtifactSources, error) {
	const artifactsConfigFile = "artifacts.yaml"

	b, err := os.ReadFile(artifactsConfigFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("artifact sources file '%s' does not exist", artifactsConfigFile)
		}

		return nil, fmt.Errorf("reading artifact sources file: %w", err)
	}

	var sources image.ArtifactSources
	if err = yaml.Unmarshal(b, &sources); err != nil {
		return nil, fmt.Errorf("decoding artifacts sources: %w", err)
	}

	return &sources, nil
}

// Assembles the image build context with user-provided values and implementation defaults.
func buildContext(buildDir, combustionDir, artefactsDir, configDir string, imageDefinition *image.Definition, artifactSources *image.ArtifactSources) *image.Context {
	ctx := &image.Context{
		ImageConfigDir:  configDir,
		BuildDir:        buildDir,
		CombustionDir:   combustionDir,
		ArtefactsDir:    artefactsDir,
		ImageDefinition: imageDefinition,
		ArtifactSources: artifactSources,
	}
	return ctx
}
