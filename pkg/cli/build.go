// Copyright 2022 Chainguard, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"chainguard.dev/apko/pkg/build"
	"chainguard.dev/apko/pkg/build/oci"
	"chainguard.dev/apko/pkg/build/types"
	"chainguard.dev/apko/pkg/sbom"
)

func Build() *cobra.Command {
	var useProot bool
	var buildDate string
	var buildArch string
	var writeSBOM bool
	var sbomPath string
	var sbomFormats []string
	var extraKeys []string
	var extraRepos []string

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build an image from a YAML configuration file",
		Long: `Build an image from a YAML configuration file.

The generated image is in a format which can be used with the "docker load"
command, e.g.

  # docker load < output.tar

Along the image, apko will generate CycloneDX and SPDX SBOMs (software 
bill of materials) describing the image contents.
`,
		Example: `  apko build <config.yaml> <tag> <output.tar>`,
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !writeSBOM {
				sbomFormats = []string{}
			}
			return BuildCmd(cmd.Context(), args[1], args[2],
				build.WithConfig(args[0]),
				build.WithProot(useProot),
				build.WithBuildDate(buildDate),
				build.WithAssertions(build.RequireGroupFile(true), build.RequirePasswdFile(true)),
				build.WithSBOM(sbomPath),
				build.WithSBOMFormats(sbomFormats),
				build.WithExtraKeys(extraKeys),
				build.WithTags(args[1]),
				build.WithExtraRepos(extraRepos),
				build.WithArch(types.ParseArchitecture(buildArch)),
			)
		},
	}

	cmd.Flags().BoolVar(&useProot, "use-proot", false, "use proot to simulate privileged operations")
	cmd.Flags().StringVar(&buildDate, "build-date", "", "date used for the timestamps of the files inside the image in RFC3339 format")
	cmd.Flags().BoolVar(&writeSBOM, "sbom", true, "generate SBOMs")
	cmd.Flags().StringVar(&sbomPath, "sbom-path", "", "generate SBOMs in dir (defaults to image directory)")
	cmd.Flags().StringVar(&buildArch, "build-arch", runtime.GOARCH, "architecture to build for -- default is Go runtime architecture")
	cmd.Flags().StringSliceVarP(&extraKeys, "keyring-append", "k", []string{}, "path to extra keys to include in the keyring")
	cmd.Flags().StringSliceVar(&sbomFormats, "sbom-formats", sbom.DefaultOptions.Formats, "SBOM formats to output")
	cmd.Flags().StringSliceVarP(&extraRepos, "repository-append", "r", []string{}, "path to extra repositories to include")

	return cmd
}

func BuildCmd(ctx context.Context, imageRef, outputTarGZ string, opts ...build.Option) error {
	wd, err := os.MkdirTemp("", "apko-*")
	if err != nil {
		return fmt.Errorf("failed to create working directory: %w", err)
	}
	defer os.RemoveAll(wd)

	bc, err := build.New(wd, opts...)
	if err != nil {
		return err
	}

	if err := bc.Refresh(); err != nil {
		return err
	}

	if bc.Options.SBOMPath == "" {
		dir, err := filepath.Abs(outputTarGZ)
		if err != nil {
			return fmt.Errorf("resolving output file path: %w", err)
		}
		bc.Options.SBOMPath = filepath.Dir(dir)
	}

	if len(bc.ImageConfiguration.Archs) != 0 {
		log.Printf("WARNING: ignoring archs in config, only building for current arch (%s)", bc.Options.Arch)
	}

	bc.Options.Log.Printf("building image '%s'", imageRef)

	layerTarGZ, err := bc.BuildLayer()
	if err != nil {
		return fmt.Errorf("failed to build layer image: %w", err)
	}
	defer os.Remove(layerTarGZ)

	if err := bc.GenerateSBOM(); err != nil {
		return fmt.Errorf("generating SBOMs: %w", err)
	}

	if err := oci.BuildImageTarballFromLayer(
		imageRef, layerTarGZ, outputTarGZ, bc.ImageConfiguration, bc.Options.SourceDateEpoch, bc.Options.Arch,
		log.Default(),
	); err != nil {
		return fmt.Errorf("failed to build OCI image: %w", err)
	}

	return nil
}
